package db

import (
	"reflect"
	"testing"
)

// A quoted table name may hold spaces, be a keyword, or be qualified by a
// quoted schema. A tokenizer that strips the quotes and hands the content on
// as a bare word is defeated by a space or a keyword, so
// `CREATE TABLE "Line Items"` would produce no table at all.
func TestQuotedTableNames(t *testing.T) {
	for _, tc := range []struct {
		sql  string
		want string
	}{
		{`CREATE TABLE "Line Items" (order_id integer)`, "line items"},
		{"CREATE TABLE `Line Items` (order_id integer)", "line items"},
		{`CREATE TABLE [Line Items] (order_id integer)`, "line items"},
		{`CREATE TABLE "shop"."Line Items" (order_id integer)`, "line items"},
		{`CREATE TABLE shop."Line Items" (order_id integer)`, "line items"},
		{`CREATE TABLE "shop".orders (order_id integer)`, "orders"},
		{"CREATE TABLE `shop`.`Line Items` (order_id integer)", "line items"},
		{`CREATE TABLE [dbo].[Line Items] (order_id integer)`, "line items"},
		{`CREATE TABLE IF NOT EXISTS "Line Items" (order_id integer)`, "line items"},
		{`CREATE TABLE "order" (id integer)`, "order"}, // a quoted keyword is a name
		{`SELECT * FROM "Line Items" WHERE id = 1`, "line items"},
		{`INSERT INTO "shop"."Line Items" (sku) VALUES ('a')`, "line items"},
		{`UPDATE [Line Items] SET sku = 'b' WHERE id = 1`, "line items"},
		{`DELETE FROM ` + "`Line Items`" + ` WHERE id = 1`, "line items"},
		{`DROP TABLE "Line Items"`, "line items"},
	} {
		st, ok := Parse(tc.sql)
		if !ok || len(st.Tables) != 1 || st.Tables[0] != tc.want {
			t.Errorf("%s\n got tables %v (ok=%v), want [%s]", tc.sql, st.Tables, ok, tc.want)
		}
	}
}

// Columns follow the same rule as tables, and a column list is read past a
// quoted name with a space in it.
func TestQuotedColumnNames(t *testing.T) {
	st, ok := Parse(`CREATE TABLE "Line Items" ("Order Id" integer, ` + "`unit price`" + ` numeric(10, 2), [Sku Code] text, PRIMARY KEY ("Order Id"))`)
	if !ok {
		t.Fatal("not parsed")
	}
	want := []string{"order id", "unit price", "sku code"}
	if !reflect.DeepEqual(st.Columns, want) {
		t.Errorf("columns = %v, want %v", st.Columns, want)
	}

	st, _ = Parse(`INSERT INTO t ("Order Id", sku) VALUES (1, 'a')`)
	if want := []string{"order id", "sku"}; !reflect.DeepEqual(st.Columns, want) {
		t.Errorf("insert columns = %v, want %v", st.Columns, want)
	}
}

// What the quotes must not let through: a bare keyword or a bare word with a
// character no dialect allows is still not a name, and a subscript is not a
// quoted identifier.
func TestQuotingDoesNotInventTables(t *testing.T) {
	for _, s := range []string{
		`SELECT FROM WHERE`,
		`DROP TABLE "unterminated`,
		`SELECT a FROM ""`,
	} {
		if st, ok := Parse(s); ok {
			t.Errorf("%q: invented tables %v", s, st.Tables)
		}
	}
	for in, want := range map[string]bool{
		`"Line Items"`:         true,
		`"shop"."Line Items"`:  true,
		"`order`":              true,
		"[dbo].[Orders]":       true,
		`shop.`:                false,
		`.orders`:              false,
		`"a b`:                 false,
		`a b`:                  false,
		`a..b`:                 false,
		`order`:                false,
		`flex-1`:               false,
		`public.transactions`:  true,
		`"shop"."Line Items".`: false,
	} {
		if got := isIdentifier(in); got != want {
			t.Errorf("isIdentifier(%q) = %v, want %v", in, got, want)
		}
	}
}

// A table a file only drops, alters or indexes is used there, not declared
// there; a CREATE TABLE declares it. Existing is what the symbol builder reads.
func TestExistingMarksStatementsThatDoNotDeclare(t *testing.T) {
	for _, tc := range []struct {
		sql      string
		existing bool
	}{
		{"CREATE TABLE t (id int)", false},
		{"CREATE TABLE IF NOT EXISTS t (id int)", false},
		{"CREATE TEMPORARY TABLE t (id int)", false},
		{"CREATE TABLE t AS SELECT 1 FROM u", false},
		{"DROP TABLE t", true},
		{"DROP TABLE IF EXISTS `t`", true},
		{"ALTER TABLE t ADD COLUMN c text", true},
		{"CREATE INDEX ix ON t (c)", true},
		{"CREATE UNIQUE INDEX ix ON t (c)", true},
	} {
		st, ok := Parse(tc.sql)
		if !ok || st.Access != AccessDDL || len(st.Tables) != 1 || st.Tables[0] != "t" {
			t.Errorf("%s: parsed as %+v (ok=%v)", tc.sql, st, ok)
			continue
		}
		if st.Existing != tc.existing {
			t.Errorf("%s: Existing = %v, want %v", tc.sql, st.Existing, tc.existing)
		}
	}
	// Reads and writes keep working, and are never DDL.
	for _, s := range []string{"TRUNCATE TABLE t", "INSERT INTO t (a) VALUES (1)", "SELECT a FROM t WHERE a = 1"} {
		st, ok := Parse(s)
		if !ok || st.Access == AccessDDL || st.Existing {
			t.Errorf("%s: parsed as %+v", s, st)
		}
	}
}
