package analyze

import (
	"fmt"
	"reflect"
	"strings"
	"testing"

	"github.com/sutrakam/graphlume/pkg/model"
)

// tablesDeclared lists the table symbols a .sql file declares as "name@line".
func tablesDeclared(src string) []string {
	var out []string
	for _, s := range File("schema.sql", []byte(src), nil).Symbols {
		if s.Kind == model.KindTable {
			out = append(out, fmt.Sprintf("%s@%d", s.Name, s.Range.Start.Line))
		}
	}
	return out
}

// A statement is reported on the line of its first real character. A comment
// above it is part of the chunk the splitter cuts at the previous semicolon,
// and would pull the statement up to the comment's line.
func TestStatementLineSkipsLeadingComments(t *testing.T) {
	src := "-- MySQL style: one statement per object.\n" + // 1
		"\n" + // 2
		"CREATE TABLE IF NOT EXISTS `accounts` (\n" + // 3
		"  `id` int\n" + // 4
		");\n" + // 5
		"\n" + // 6
		"/* a block\n" + // 7
		"   comment */\n" + // 8
		"-- and a line one\n" + // 9
		"CREATE TABLE transfers (id int);\n" + // 10
		"INSERT INTO accounts (id) VALUES (1); CREATE TABLE audit (id int);\n" // 11
	want := []string{"accounts@3", "transfers@10", "audit@11"}
	if got := tablesDeclared(src); !reflect.DeepEqual(got, want) {
		t.Errorf("got %v, want %v", got, want)
	}
}

// A table is declared where it is created. A file that drops, alters, indexes,
// truncates, reads or writes a table uses it there; the use is still an access.
func TestOnlyCreateTableDeclaresATable(t *testing.T) {
	src := strings.Join([]string{
		"CREATE TABLE kept (id int);",
		"DROP TABLE IF EXISTS `scratch`;",
		"ALTER TABLE ghost ADD COLUMN c text;",
		"CREATE INDEX ix_other ON other (c);",
		"TRUNCATE TABLE wiped;",
		"INSERT INTO written (a) VALUES (1);",
		"SELECT a FROM readonly;",
		`CREATE TABLE "Line Items" (order_id int);`,
	}, "\n")
	want := []string{"kept@1", "line items@8"}
	if got := tablesDeclared(src); !reflect.DeepEqual(got, want) {
		t.Errorf("declared %v, want %v", got, want)
	}

	// The uses are all still recorded, so a data-access edge can bind to a
	// table declared in another file, and an undeclared one is reported as such.
	used := map[string]string{}
	for _, a := range File("schema.sql", []byte(src), nil).Accesses {
		used[a.Table] = string(a.Mode)
	}
	for table, mode := range map[string]string{
		"kept": "DDL", "scratch": "DDL", "ghost": "DDL", "other": "DDL",
		"wiped": "WRITE", "written": "WRITE", "readonly": "READ", "line items": "DDL",
	} {
		if used[table] != mode {
			t.Errorf("access to %s = %q, want %q (all: %v)", table, used[table], mode, used)
		}
	}
}
