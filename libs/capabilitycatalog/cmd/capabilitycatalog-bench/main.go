// Command capabilitycatalog-bench measures what it costs to load the UI
// capability catalog, so the numbers ADR-013 rests on are reproducible
// rather than quoted.
//
// Usage:
//
//	go run ./libs/capabilitycatalog/cmd/capabilitycatalog-bench \
//	    -n 60000 -modules 20 -scope module -format artifact
//
// One process per measurement, because peak RSS is a property of a process:
// reading /proc/self/status's VmHWM after two loads in one process would
// report the larger, not each.
//
// -scope module is what the ADS does on a capability request (one module);
// -scope catalog is what the Administration Service's impact index does at
// startup (everything). -format yaml parses the authored source; -format
// artifact decodes the pre-decoded gob beside it.
package main

import (
	"bufio"
	"flag"
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"github.com/tishan-harischandra/cerbos-poc/libs/capabilitycatalog"
)

// leavesPerCapability matches the committed catalog's measured shape, which
// ADR-013 records as 2.2 leaves per capability. Expressed as a repeating
// 2,2,2,2,3 cycle so a synthesised catalog has the same average without
// every capability being identical.
var leafCycle = []int{2, 2, 2, 2, 3}

func main() {
	n := flag.Int("n", 400, "how many capabilities to synthesise")
	modules := flag.Int("modules", 20, "how many modules to spread them across")
	scope := flag.String("scope", "module", "module (what the ADS loads) or catalog (what the impact index loads)")
	format := flag.String("format", "yaml", "yaml or artifact")
	flag.Parse()

	dir, err := os.MkdirTemp("", "capability-bench-")
	if err != nil {
		fmt.Fprintf(os.Stderr, "capabilitycatalog-bench: %v\n", err)
		os.Exit(1)
	}
	defer os.RemoveAll(dir)

	if err := synthesise(dir, *n, *modules); err != nil {
		fmt.Fprintf(os.Stderr, "capabilitycatalog-bench: synthesising: %v\n", err)
		os.Exit(1)
	}

	// Building the artifact is a sync-time cost, not a load-time one, so it
	// happens before the clock starts.
	if *format == "artifact" {
		if err := capabilitycatalog.BuildModuleArtifacts(dir); err != nil {
			fmt.Fprintf(os.Stderr, "capabilitycatalog-bench: building artifacts: %v\n", err)
			os.Exit(1)
		}
	}

	start := time.Now()
	var loaded int
	switch *scope {
	case "module":
		defs, err := capabilitycatalog.LoadDefinitionsForModule(dir, moduleName(0))
		if err != nil {
			fmt.Fprintf(os.Stderr, "capabilitycatalog-bench: %v\n", err)
			os.Exit(1)
		}
		loaded = len(defs)
	case "catalog":
		defs, err := capabilitycatalog.LoadDefinitionsDir(dir)
		if err != nil {
			fmt.Fprintf(os.Stderr, "capabilitycatalog-bench: %v\n", err)
			os.Exit(1)
		}
		loaded = len(defs)
	default:
		fmt.Fprintf(os.Stderr, "capabilitycatalog-bench: unknown -scope %q\n", *scope)
		os.Exit(1)
	}
	elapsed := time.Since(start)

	peak, err := peakRSS()
	if err != nil {
		fmt.Fprintf(os.Stderr, "capabilitycatalog-bench: %v\n", err)
		os.Exit(1)
	}

	fmt.Printf("n=%d modules=%d scope=%s format=%s loaded=%d elapsed=%s peak_rss=%.1fMiB\n",
		*n, *modules, *scope, *format, loaded, elapsed.Round(time.Millisecond), peak)
}

func moduleName(i int) string { return fmt.Sprintf("module_%02d", i) }

// synthesise writes n capabilities spread evenly across modules, in the
// authored layout: one directory per module.
func synthesise(dir string, n, modules int) error {
	perModule := make([][]string, modules)
	for i := 0; i < n; i++ {
		m := i % modules
		var b strings.Builder
		fmt.Fprintf(&b, "  - key: module_%02d.capability.%06d\n", m, i)
		fmt.Fprintf(&b, "    module: %s\n", moduleName(m))
		b.WriteString("    context: INSTANCE\n")
		b.WriteString("    expression:\n      allOf:\n")
		for leaf := 0; leaf < leafCycle[i%len(leafCycle)]; leaf++ {
			b.WriteString("        - permission:\n")
			fmt.Fprintf(&b, "            resource: resource_%06d\n", (i+leaf)%2000)
			b.WriteString("            action: read\n")
			b.WriteString("            targetRef: subject\n")
		}
		perModule[m] = append(perModule[m], b.String())
	}

	for m := 0; m < modules; m++ {
		moduleDir := filepath.Join(dir, moduleName(m))
		if err := os.MkdirAll(moduleDir, 0o755); err != nil {
			return err
		}
		var doc strings.Builder
		doc.WriteString("catalogRevision: 1\ncapabilities:\n")
		for _, c := range perModule[m] {
			doc.WriteString(c)
		}
		if err := os.WriteFile(filepath.Join(moduleDir, "generated.yaml"), []byte(doc.String()), 0o644); err != nil {
			return err
		}
	}
	return nil
}

// peakRSS reads VmHWM - the high-water mark of resident set size - from
// /proc/self/status. This is the number a container memory limit is
// enforced against, which runtime.MemStats does not report.
func peakRSS() (float64, error) {
	f, err := os.Open("/proc/self/status")
	if err != nil {
		return 0, fmt.Errorf("reading peak RSS: %w", err)
	}
	defer f.Close()

	scanner := bufio.NewScanner(f)
	for scanner.Scan() {
		line := scanner.Text()
		if !strings.HasPrefix(line, "VmHWM:") {
			continue
		}
		fields := strings.Fields(line)
		if len(fields) < 2 {
			return 0, fmt.Errorf("unexpected VmHWM line %q", line)
		}
		kib, err := strconv.ParseFloat(fields[1], 64)
		if err != nil {
			return 0, fmt.Errorf("parsing VmHWM %q: %w", fields[1], err)
		}
		return kib / 1024, nil
	}
	return 0, fmt.Errorf("no VmHWM in /proc/self/status")
}
