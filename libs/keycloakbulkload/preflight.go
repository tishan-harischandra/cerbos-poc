package keycloakbulkload

import (
	"bufio"
	"fmt"
	"os"
	"strconv"
	"strings"
	"syscall"
)

// PreflightEstimate is the resource budget one BulkLoad run needs, derived
// from the population it is about to write.
type PreflightEstimate struct {
	Users int
	// RoleMappings is the total number of user_role_mapping rows.
	RoleMappings int
	// Memberships is the total number of user_group_membership rows
	// (issue #87): 5 realms x 600,000 users x 2 hospitals each is
	// 6,000,000 rows, the same order of magnitude as the role mappings
	// this estimate already accounts for, so a five-realm population's
	// disk estimate must count them too.
	Memberships int
}

// bytesPerUser and bytesPerMapping are conservative, measured-order-of-
// magnitude row-size estimates (row payload plus each table's own indexes),
// used only to size the preflight check's refusal threshold. They are
// deliberately rounded up: a preflight check that is slightly too
// pessimistic wastes disk headroom the operator already had to provision
// anyway; one that is too optimistic lets a run fail hours in in with a full
// disk instead of refusing to start.
const (
	bytesPerUser    = 2048 // user_entity + two user_attribute rows + one credential row, plus indexes
	bytesPerMapping = 96   // one user_role_mapping row plus its primary key index
	// bytesPerMembership mirrors bytesPerMapping: user_group_membership is
	// the same two-column-plus-a-string shape as user_role_mapping, with
	// the same primary key index cost.
	bytesPerMembership = 96

	// minFreeRAMBytes is the smallest amount of available RAM this package
	// will start a run under. BulkLoad itself holds at most one
	// DefaultBatchSize-sized batch in memory; this floor exists for
	// PostgreSQL's own COPY working memory and WAL buffering on the other
	// end of the connection, not for this process.
	minFreeRAMBytes = 2 << 30 // 2 GiB

	// diskHeadroomFactor inflates the raw row-size estimate: PostgreSQL's
	// MVCC bloat, WAL, and the operator's own headroom for a second run
	// without first vacuuming all make the naive row-count estimate an
	// underestimate of what a comfortable run actually needs.
	diskHeadroomFactor = 3
)

// EstimatedDiskBytes is how much disk PreflightEstimate expects the run to
// consume, before diskHeadroomFactor.
func (e PreflightEstimate) rawBytes() int64 {
	return int64(e.Users)*bytesPerUser + int64(e.RoleMappings)*bytesPerMapping + int64(e.Memberships)*bytesPerMembership
}

// EstimatedDiskBytes is the disk footprint Preflight refuses to start
// without, including headroom.
func (e PreflightEstimate) EstimatedDiskBytes() int64 {
	return e.rawBytes() * diskHeadroomFactor
}

// Preflight refuses to start a run when the host does not have enough free
// RAM or disk for it, with an actionable message naming the shortfall - the
// acceptance criterion is "refuses to start... with an actionable message",
// not merely a log line a human has to go looking for.
//
// dataDir is the filesystem path whose free space is checked - the
// PostgreSQL data directory the target database's disk actually lives on,
// not this process' own working directory.
func Preflight(estimate PreflightEstimate, dataDir string) error {
	needDisk := estimate.EstimatedDiskBytes()
	freeDisk, err := freeDiskBytes(dataDir)
	if err != nil {
		return fmt.Errorf("keycloakbulkload: checking free disk on %s: %w", dataDir, err)
	}
	if freeDisk < needDisk {
		return fmt.Errorf(
			"keycloakbulkload: refusing to start - %s has %s free but this population "+
				"(%d users, %d role mappings, %d organization memberships) needs at least %s "+
				"(including %dx headroom for MVCC bloat and WAL); free up disk or reduce the "+
				"population and try again",
			dataDir, humanBytes(freeDisk), estimate.Users, estimate.RoleMappings, estimate.Memberships,
			humanBytes(needDisk), diskHeadroomFactor)
	}

	freeRAM, err := freeRAMBytes()
	if err != nil {
		return fmt.Errorf("keycloakbulkload: checking free RAM: %w", err)
	}
	if freeRAM < minFreeRAMBytes {
		return fmt.Errorf(
			"keycloakbulkload: refusing to start - only %s RAM is available, below the %s "+
				"minimum this package needs for PostgreSQL's own COPY working memory and WAL "+
				"buffering; free up memory and try again",
			humanBytes(freeRAM), humanBytes(minFreeRAMBytes))
	}

	return nil
}

func freeDiskBytes(path string) (int64, error) {
	var stat syscall.Statfs_t
	if err := syscall.Statfs(path, &stat); err != nil {
		return 0, err
	}
	return int64(stat.Bavail) * int64(stat.Bsize), nil //nolint:unconvert // Bsize's width is platform-dependent
}

// freeRAMBytes reads /proc/meminfo's MemAvailable, the kernel's own estimate
// of memory a new process could use without swapping - more accurate than
// MemFree, which excludes reclaimable cache.
func freeRAMBytes() (int64, error) {
	f, err := os.Open("/proc/meminfo")
	if err != nil {
		return 0, err
	}
	defer f.Close()

	scanner := bufio.NewScanner(f)
	for scanner.Scan() {
		line := scanner.Text()
		if !strings.HasPrefix(line, "MemAvailable:") {
			continue
		}
		fields := strings.Fields(line)
		if len(fields) < 2 {
			return 0, fmt.Errorf("keycloakbulkload: unexpected /proc/meminfo line: %q", line)
		}
		kib, err := strconv.ParseInt(fields[1], 10, 64)
		if err != nil {
			return 0, fmt.Errorf("keycloakbulkload: parsing /proc/meminfo: %w", err)
		}
		return kib * 1024, nil
	}
	return 0, fmt.Errorf("keycloakbulkload: /proc/meminfo has no MemAvailable line")
}

func humanBytes(n int64) string {
	const unit = 1024
	if n < unit {
		return fmt.Sprintf("%dB", n)
	}
	div, exp := int64(unit), 0
	for v := n / unit; v >= unit; v /= unit {
		div *= unit
		exp++
	}
	return fmt.Sprintf("%.1f%ciB", float64(n)/float64(div), "KMGTPE"[exp])
}
