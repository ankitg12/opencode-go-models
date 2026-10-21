package main

import (
	"fmt"
	"os"
	"strconv"
	"strings"
	"text/tabwriter"
)

func runQuery(path, query string) error {
	if _, err := os.Stat(path); err != nil {
		return fmt.Errorf("cache not found at %s — run with --refresh first", path)
	}
	sqlDB, err := openDB(path, true)
	if err != nil {
		return err
	}
	defer sqlDB.Close()
	rows, err := sqlDB.Query(query)
	if err != nil {
		return err
	}
	defer rows.Close()
	columns, err := rows.Columns()
	if err != nil {
		return err
	}
	tw := tabwriter.NewWriter(os.Stdout, 0, 2, 2, ' ', 0)
	fmt.Fprintln(tw, strings.Join(columns, "\t"))
	separators := make([]string, len(columns))
	for i := range separators {
		separators[i] = "───"
	}
	fmt.Fprintln(tw, strings.Join(separators, "\t"))
	count := 0
	for rows.Next() {
		values := make([]any, len(columns))
		pointers := make([]any, len(columns))
		for i := range values {
			pointers[i] = &values[i]
		}
		if err := rows.Scan(pointers...); err != nil {
			return err
		}
		cells := make([]string, len(columns))
		for i, value := range values {
			cells[i] = cell(value)
		}
		fmt.Fprintln(tw, strings.Join(cells, "\t"))
		count++
	}
	if err := rows.Err(); err != nil {
		return err
	}
	tw.Flush()
	fmt.Printf("\n(%d rows)\n", count)
	return nil
}

func cell(value any) string {
	switch value := value.(type) {
	case nil:
		return ""
	case []byte:
		return string(value)
	case float64:
		return strconv.FormatFloat(value, 'g', -1, 64)
	case int64:
		return strconv.FormatInt(value, 10)
	default:
		return fmt.Sprintf("%v", value)
	}
}
