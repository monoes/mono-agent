// cmd/monoagentcli/tablewriter_helper.go
package main

import (
	"io"

	"github.com/olekukonko/tablewriter"
	"github.com/olekukonko/tablewriter/renderer"
	"github.com/olekukonko/tablewriter/tw"
)

// newPlainTable builds a *tablewriter.Table matching this CLI's established
// look from before tablewriter v1: no box-drawing borders, no auto-wrapping
// of long cell content. v1 removed the old NewWriter+SetHeader+
// SetBorder(false)+SetAutoWrapText(false) setter-based API entirely in
// favor of this Rendition/Config builder shape (SetHeader/SetBorder/
// SetAutoWrapText/SetColumnAlignment and the ALIGN_* constants no longer
// exist on *tablewriter.Table at all -- verified directly against v1.1.4's
// source, not just its changelog).
//
// perColumnAlign is optional (nil for the default left-alignment
// everywhere, matching every call site that never called
// SetColumnAlignment) -- pass one tw.Align per header column to right-
// align a "Field"/"Value"-style table's first column, matching the four
// call sites that used SetColumnAlignment([]int{ALIGN_RIGHT, ALIGN_LEFT}).
func newPlainTable(w io.Writer, headers []string, perColumnAlign []tw.Align) *tablewriter.Table {
	cellFmt := tw.CellFormatting{AutoWrap: tw.WrapNone}
	// v1's own default row alignment is centered (confirmed directly by
	// rendering a table with no alignment override) -- v0.0.5's default was
	// left-aligned, which every call site with no SetColumnAlignment call
	// implicitly relied on. Restore that default explicitly here; only
	// override it with per-column alignment when the caller asks for it.
	rowCfg := tw.CellConfig{Formatting: cellFmt, Alignment: tw.CellAlignment{Global: tw.AlignLeft}}
	if len(perColumnAlign) > 0 {
		rowCfg.Alignment = tw.CellAlignment{PerColumn: perColumnAlign}
	}
	table := tablewriter.NewTable(w,
		tablewriter.WithRenderer(renderer.NewBlueprint(tw.Rendition{Borders: tw.BorderNone})),
		tablewriter.WithConfig(tablewriter.Config{
			Header: tw.CellConfig{Formatting: cellFmt},
			Row:    rowCfg,
		}),
	)
	table.Header(headers)
	return table
}
