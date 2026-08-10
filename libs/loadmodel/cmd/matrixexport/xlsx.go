// Minimal .xlsx writer: enough of SpreadsheetML to emit a workbook of plain
// string/number grids, with no third-party dependency. Every cell is written
// as an inline string or a number, so there is no shared-string table to keep
// consistent.
package main

import (
	"archive/zip"
	"encoding/xml"
	"fmt"
	"io"
	"strings"
)

// sheet is one worksheet: a name and a rectangular-ish grid of cells. Rows may
// be ragged; short rows simply end early.
type sheet struct {
	name string
	rows [][]string
	// freezeAt is the number of leading rows and columns to freeze, so a
	// wide matrix stays readable while scrolling. Zero means no freeze.
	freezeRows int
	freezeCols int
}

func writeWorkbook(w io.Writer, sheets []sheet) error {
	zw := zip.NewWriter(w)

	if err := addFile(zw, "[Content_Types].xml", contentTypes(len(sheets))); err != nil {
		return err
	}
	if err := addFile(zw, "_rels/.rels", rootRels()); err != nil {
		return err
	}
	if err := addFile(zw, "xl/workbook.xml", workbookXML(sheets)); err != nil {
		return err
	}
	if err := addFile(zw, "xl/_rels/workbook.xml.rels", workbookRels(len(sheets))); err != nil {
		return err
	}
	for i, s := range sheets {
		if err := addFile(zw, fmt.Sprintf("xl/worksheets/sheet%d.xml", i+1), sheetXML(s)); err != nil {
			return err
		}
	}
	return zw.Close()
}

func addFile(zw *zip.Writer, name, body string) error {
	f, err := zw.Create(name)
	if err != nil {
		return fmt.Errorf("creating %s: %w", name, err)
	}
	if _, err := io.WriteString(f, body); err != nil {
		return fmt.Errorf("writing %s: %w", name, err)
	}
	return nil
}

func contentTypes(sheetCount int) string {
	var b strings.Builder
	b.WriteString(xml.Header)
	b.WriteString(`<Types xmlns="http://schemas.openxmlformats.org/package/2006/content-types">`)
	b.WriteString(`<Default Extension="rels" ContentType="application/vnd.openxmlformats-package.relationships+xml"/>`)
	b.WriteString(`<Default Extension="xml" ContentType="application/xml"/>`)
	b.WriteString(`<Override PartName="/xl/workbook.xml" ContentType="application/vnd.openxmlformats-officedocument.spreadsheetml.sheet.main+xml"/>`)
	for i := 1; i <= sheetCount; i++ {
		fmt.Fprintf(&b, `<Override PartName="/xl/worksheets/sheet%d.xml" ContentType="application/vnd.openxmlformats-officedocument.spreadsheetml.worksheet+xml"/>`, i)
	}
	b.WriteString(`</Types>`)
	return b.String()
}

func rootRels() string {
	return xml.Header +
		`<Relationships xmlns="http://schemas.openxmlformats.org/package/2006/relationships">` +
		`<Relationship Id="rId1" Type="http://schemas.openxmlformats.org/officeDocument/2006/relationships/officeDocument" Target="xl/workbook.xml"/>` +
		`</Relationships>`
}

func workbookXML(sheets []sheet) string {
	var b strings.Builder
	b.WriteString(xml.Header)
	b.WriteString(`<workbook xmlns="http://schemas.openxmlformats.org/spreadsheetml/2006/main" xmlns:r="http://schemas.openxmlformats.org/officeDocument/2006/relationships"><sheets>`)
	for i, s := range sheets {
		fmt.Fprintf(&b, `<sheet name="%s" sheetId="%d" r:id="rId%d"/>`, escape(s.name), i+1, i+1)
	}
	b.WriteString(`</sheets></workbook>`)
	return b.String()
}

func workbookRels(sheetCount int) string {
	var b strings.Builder
	b.WriteString(xml.Header)
	b.WriteString(`<Relationships xmlns="http://schemas.openxmlformats.org/package/2006/relationships">`)
	for i := 1; i <= sheetCount; i++ {
		fmt.Fprintf(&b, `<Relationship Id="rId%d" Type="http://schemas.openxmlformats.org/officeDocument/2006/relationships/worksheet" Target="worksheets/sheet%d.xml"/>`, i, i)
	}
	b.WriteString(`</Relationships>`)
	return b.String()
}

func sheetXML(s sheet) string {
	var b strings.Builder
	b.WriteString(xml.Header)
	b.WriteString(`<worksheet xmlns="http://schemas.openxmlformats.org/spreadsheetml/2006/main">`)
	if s.freezeRows > 0 || s.freezeCols > 0 {
		fmt.Fprintf(&b, `<sheetViews><sheetView workbookViewId="0"><pane xSplit="%d" ySplit="%d" topLeftCell="%s%d" activePane="bottomRight" state="frozen"/></sheetView></sheetViews>`,
			s.freezeCols, s.freezeRows, columnName(s.freezeCols+1), s.freezeRows+1)
	}
	b.WriteString(`<sheetData>`)
	for r, row := range s.rows {
		fmt.Fprintf(&b, `<row r="%d">`, r+1)
		for c, value := range row {
			if value == "" {
				continue
			}
			ref := fmt.Sprintf("%s%d", columnName(c+1), r+1)
			fmt.Fprintf(&b, `<c r="%s" t="inlineStr"><is><t xml:space="preserve">%s</t></is></c>`, ref, escape(value))
		}
		b.WriteString(`</row>`)
	}
	b.WriteString(`</sheetData></worksheet>`)
	return b.String()
}

// columnName converts a 1-based column index to its spreadsheet letters.
func columnName(index int) string {
	var name []byte
	for index > 0 {
		index--
		name = append([]byte{byte('A' + index%26)}, name...)
		index /= 26
	}
	return string(name)
}

func escape(s string) string {
	var b strings.Builder
	if err := xml.EscapeText(&b, []byte(s)); err != nil {
		// xml.EscapeText only fails if the writer fails, and
		// strings.Builder never does.
		panic(err)
	}
	return b.String()
}
