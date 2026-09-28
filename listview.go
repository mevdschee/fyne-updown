package main

import (
	"sort"
	"strings"

	"fyne.io/fyne/v2"
	"fyne.io/fyne/v2/theme"
	"fyne.io/fyne/v2/widget"
)

type column struct {
	title   string
	width   float32
	numeric bool
}

// row holds the text of each cell and, for numeric columns, the value to
// sort on.
type row struct {
	text   []string
	values []float64
}

// listView is a table with clickable headers to change the sort order.
type listView struct {
	columns  []column
	rows     []row
	sortCol  int
	sortDesc bool
	table    *widget.Table
}

func newListView(columns []column, sortCol int, sortDesc bool) *listView {
	l := &listView{columns: columns, sortCol: sortCol, sortDesc: sortDesc}
	l.table = widget.NewTableWithHeaders(
		func() (int, int) { return len(l.rows), len(l.columns) },
		func() fyne.CanvasObject {
			label := widget.NewLabel("")
			label.Truncation = fyne.TextTruncateEllipsis
			return label
		},
		func(id widget.TableCellID, o fyne.CanvasObject) {
			label := o.(*widget.Label)
			label.Alignment = fyne.TextAlignLeading
			if l.columns[id.Col].numeric {
				label.Alignment = fyne.TextAlignTrailing
			}
			label.SetText(l.rows[id.Row].text[id.Col])
		},
	)
	l.table.ShowHeaderColumn = false
	// rows are for reading only, until blocking and shaping are added
	l.table.OnSelected = func(id widget.TableCellID) { l.table.UnselectAll() }
	l.table.CreateHeader = func() fyne.CanvasObject {
		b := widget.NewButton("", nil)
		b.Importance = widget.LowImportance
		b.IconPlacement = widget.ButtonIconTrailingText
		return b
	}
	l.table.UpdateHeader = func(id widget.TableCellID, o fyne.CanvasObject) {
		b := o.(*widget.Button)
		col := id.Col
		if col < 0 {
			return
		}
		b.SetText(l.columns[col].title)
		switch {
		case col != l.sortCol:
			b.SetIcon(nil)
		case l.sortDesc:
			b.SetIcon(theme.MenuDropDownIcon())
		default:
			b.SetIcon(theme.MenuDropUpIcon())
		}
		b.OnTapped = func() { l.sortBy(col) }
	}
	for i, c := range columns {
		l.table.SetColumnWidth(i, c.width)
	}
	return l
}

func (l *listView) sortBy(col int) {
	if col == l.sortCol {
		l.sortDesc = !l.sortDesc
	} else {
		// numbers are most interesting from high to low, names from A to Z
		l.sortCol, l.sortDesc = col, l.columns[col].numeric
	}
	l.sort()
	l.table.Refresh()
}

func (l *listView) setRows(rows []row) {
	l.rows = rows
	l.sort()
	l.table.Refresh()
}

func (l *listView) sort() {
	col := l.sortCol
	numeric := l.columns[col].numeric
	sort.SliceStable(l.rows, func(i, j int) bool {
		a, b := l.rows[i], l.rows[j]
		var c int
		if numeric {
			c = compare(a.values[col], b.values[col])
		} else {
			c = strings.Compare(strings.ToLower(a.text[col]), strings.ToLower(b.text[col]))
		}
		if l.sortDesc {
			c = -c
		}
		if c == 0 {
			// keep equal rows in a fixed order so they do not jump around
			return strings.Join(a.text, "\x00") < strings.Join(b.text, "\x00")
		}
		return c < 0
	})
}

func compare(a, b float64) int {
	switch {
	case a < b:
		return -1
	case a > b:
		return 1
	}
	return 0
}
