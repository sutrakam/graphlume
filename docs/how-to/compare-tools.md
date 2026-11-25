# Comparing Code Intelligence Tools

To compare GraphLume against another code intelligence tool on the same
codebase, you need a fair, repeatable comparison evaluated against an objective
ground-truth answer key.

GraphLume provides compiler-adjudicated ground truth: call sites where the
parser recorded a call and the language compiler resolved that line to exactly
one unambiguous definition. Any code intelligence tool can be evaluated against
the same adjudicated denominator.

---

## 1. Exporting Predictions into CSV

Predictions from another tool should be formatted as a CSV file with either 3 or 4 columns:

```csv
caller_file,caller_line,callee_name,callee_file
```

| Column | Required | Description | Example |
|---|---|---|---|
| `caller_file` | Yes | Path to the file containing the call | `pkg/service.go` |
| `caller_line` | Yes | 1-based line number of the call site | `42` |
| `callee_name` | Yes | Identifier name of the resolved function/method | `ProcessRequest` |
| `callee_file` | No | Path to the file where the definition is declared | `pkg/handler.go` |

A header line is optional (if present, GraphLume skips it automatically).

---

## 2. Sampling Common Call Sites

To ensure both tools answer the same questions, you can sample $N$ random
functions or call sites from the repository using the sampler script:

```bash
# Sample 50 random call sites from the repository
python3 scripts/compare/sample.py --mode calls -n 50 -o sample.csv
```

This generates a template CSV populated with `caller_file` and `caller_line`.
You can then run the external tool on those specific locations and fill in the
predicted `callee_name` and optional `callee_file`.

---

## 3. Running the Comparison

Run `graphlume accuracy` with the `--compare` flag:

```bash
graphlume accuracy --compare predictions.csv --label "External Indexer"
```

For programmatic pipelines and CI, pass `--json`:

```bash
graphlume accuracy --compare predictions.csv --label "External Indexer" --json
```

---

## 4. How Comparison is Scored

Both GraphLume and the external tool are evaluated on the exact same ground-truth
call site set $G$:

- **True Positive (TP):** The tool predicted a target at that call site, and the
  predicted `callee_name` (and `callee_file` if supplied) matches the compiler's
  adjudicated target.
- **False Positive (FP):** The tool predicted a target at that call site, but the
  prediction did not match the compiler target.
- **Missed (FN):** The tool emitted no edge or prediction for that adjudicated call site.

The standard information retrieval metrics are reported side-by-side:

$$\text{Precision} = \frac{\text{TP}}{\text{TP} + \text{FP}}$$

$$\text{Recall} = \frac{\text{TP}}{|G|}$$

$$\text{F1} = \frac{2 \times \text{Precision} \times \text{Recall}}{\text{Precision} + \text{Recall}}$$

Because both tools are measured against the same denominator $|G|$, their recall
and F1 scores can be directly and impartially compared.
