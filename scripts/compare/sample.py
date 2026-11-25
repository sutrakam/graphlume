#!/usr/bin/env python3
"""
scripts/compare/sample.py

Samples N random functions or call sites from an indexed repository database
(.graphlume/index.db) to produce a standard comparison CSV template.

Usage:
    python3 scripts/compare/sample.py [-n 50] [--db .graphlume/index.db] [-o sample.csv]
"""

import argparse
import csv
import os
import sqlite3
import sys

def main():
    parser = argparse.ArgumentParser(
        description="Sample N random functions or call sites for cross-tool accuracy comparison."
    )
    parser.add_argument("-n", "--count", type=int, default=50, help="Number of items to sample (default: 50)")
    parser.add_argument("--db", default=os.path.join(".graphlume", "index.db"), help="Path to index.db")
    parser.add_argument("-o", "--output", default="-", help="Output CSV path (default: stdout)")
    parser.add_argument("--mode", choices=["functions", "calls"], default="functions",
                        help="Sample functions or parsed call sites (default: functions)")
    parser.add_argument("--seed", type=int, default=None, help="Random seed for deterministic sampling")

    args = parser.parse_args()

    if not os.path.isfile(args.db):
        sys.stderr.write(f"Error: Database not found at {args.db}. Run 'graphlume index' first.\n")
        sys.exit(1)

    conn = sqlite3.connect(args.db)
    cursor = conn.cursor()

    if args.seed is not None:
        import random
        random.seed(args.seed)

    if args.mode == "functions":
        query = """
        SELECT f.path, s.name, s.start_line, s.end_line
        FROM symbols s
        JOIN files f ON f.id = s.file_id
        WHERE s.kind IN ('function', 'method')
          AND s.start_line > 0
        ORDER BY RANDOM()
        LIMIT ?
        """
        cursor.execute(query, (args.count,))
        rows = cursor.fetchall()
        out_f = sys.stdout if args.output == "-" else open(args.output, "w", newline="", encoding="utf-8")
        try:
            writer = csv.writer(out_f)
            writer.writerow(["caller_file", "caller_line", "callee_name", "callee_file"])
            for r in rows:
                writer.writerow([r[0], r[2], "", ""])
        finally:
            if out_f is not sys.stdout:
                out_f.close()
    else:
        query = """
        SELECT f.path, r.start_line, r.name
        FROM symbol_references r
        JOIN files f ON f.id = r.file_id
        WHERE r.kind LIKE '%call%'
          AND r.start_line > 0
        ORDER BY RANDOM()
        LIMIT ?
        """
        cursor.execute(query, (args.count,))
        rows = cursor.fetchall()
        out_f = sys.stdout if args.output == "-" else open(args.output, "w", newline="", encoding="utf-8")
        try:
            writer = csv.writer(out_f)
            writer.writerow(["caller_file", "caller_line", "callee_name", "callee_file"])
            for r in rows:
                writer.writerow([r[0], r[1], "", ""])
        finally:
            if out_f is not sys.stdout:
                out_f.close()

if __name__ == "__main__":
    main()
