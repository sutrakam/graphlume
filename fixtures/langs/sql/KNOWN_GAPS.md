# SQL: known gaps

Tables and columns are recorded by the database scanner in `internal/analyze`, not by this grammar query, so the table losses described below affect only the views, functions and other objects that follow the unreadable statement in the same file.


The grammar is a general SQL grammar with Postgres leanings. It is exact for
plain `CREATE TABLE`, `VIEW`, `INDEX`, `FUNCTION`, `TRIGGER`, `TYPE`,
`SCHEMA` and `SEQUENCE`, and weak on dialect syntax. The weakness is not
limited to the statement that uses the syntax: an unreadable statement can
swallow the ones after it.

## Statements the grammar cannot read

- T-SQL procedures: `CREATE PROCEDURE [dbo].[ins_Thing] @id INT AS BEGIN ... END`
  is a tree of ERROR nodes with no identifier (`(ERROR (ERROR) (ERROR) ...)`),
  so nothing is extracted.
- Cassandra CQL `CREATE KEYSPACE`, MySQL `CREATE DATABASE`, `CREATE USER`,
  `CREATE ROLE`, `CREATE EXTENSION`, `CREATE DOMAIN`: not objects this query
  records.
- Postgres `CREATE UNLOGGED TABLE`, `CREATE OR REPLACE TRIGGER`,
  `CREATE RECURSIVE VIEW`, `CREATE FUNCTION IF NOT EXISTS`: parsed as errors.
  `CREATE INDEX ... USING`, `CREATE TEMP SEQUENCE` and the other common forms
  are read.
- An index named with a schema (`CREATE INDEX shop.ix ON t (c)`) is read as an
  index called `shop`; the grammar does not take the dotted name.

- **A `DO $$ ... $$` block** (Postgres anonymous code block): the grammar
  reads `DO` as an error and the body between the dollar quotes as one opaque
  `content` token, never as statements (tree: `(ERROR (ERROR) (content)
  (content))`). A `CREATE FUNCTION` written inside the block, as in the
  pgmq `after-create.sql` of supabase/postgres, is therefore not extracted, and
  a file that is only such a block yields no symbol. The body is text, so no
  query can reach it. A statement after the block is read normally. The same
  holds for the body of any other dollar-quoted string, including the body of
  a function (the SQL inside `CREATE FUNCTION ... AS $$ ... $$` is not
  parsed, which is why nothing declared inside it is a definition).

## Statements lost because of an earlier statement

- A MySQL table with an inline index clause, `KEY k (col)` or `UNIQUE KEY`,
  inside `CREATE TABLE`: the table is found and the tables after it are absorbed
  into the same `table_parameters` node (tree fragment:
  `(table_column ... (ERROR (ERROR)))`). In a real MySQL dump (the Linguist
  Sakila schema) 24 of 33 objects were lost this way.
- An `INSERT` the grammar cannot read, for example one without `VALUES` and
  with `\r\n` and `''` inside long strings, turns the rest of the file into one
  ERROR node. In a real dump 5 of 9 tables after it were lost. The query
  recovers a table when its name and first column survive as
  `(ERROR (identifier) (table_column ...))`, which saved one of the six.

## Not definitions (decided, not gaps)

Columns, constraints and index columns; see the header of `expected.tsv`.
`CREATE TABLE x AS SELECT ...` is a table.

## Overlap with the database scanner

`internal/analyze` records every table as a `table` symbol and every column as
a `column` symbol from the same file, so a `CREATE TABLE` produces both that
symbol and the `class` symbol from this query. Reported, not reconciled.
