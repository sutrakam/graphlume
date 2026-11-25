# BitBake known gaps

## A shell function containing a heredoc, and what follows it

```
gen_file() {
	...
	cat > ${WORKDIR}/out.cmake <<- EOF
	set( A ${@foo(d.getVar('X'))} )
EOF
}
```

The grammar cannot read a shell body with a heredoc and Python expansions in
it. The tree is a top-level `(ERROR (variable_assignment (identifier) (ERROR
(identifier) (flag)) ...) (shell_content) ...)` in which the function name is
gone, so `gen_file` is not extracted, and the statements after the function
(an `addtask`, the next plain assignments) are sometimes swallowed by the same
error region. In a real class file (the cmake class from the Linguist samples)
this lost one function, one `addtask`, two assignments and a second function:
5 of 40 definitions. The fixture does not contain such a function, because
the list would then depend on where error recovery happens to stop.

## A shell function read as a variable assignment

After an error, a function can come back as
`(variable_assignment (identifier) ...)` whose text is `name() {`. The query
recognises that shape by its text and records a function; `builder.bbclass`
pins it. A function that is read some other way (an ERROR with no identifier
for the name) is lost, as above.

## Not definitions (decided, not gaps)

`inherit`, `inherit_defer`, `include`, `require`, `EXPORT_FUNCTIONS`,
`addhandler`, variable flag assignments (`do_x[dirs] = ...`,
`SRC_URI[sha256sum] = ...`), `export NAME` without a value, and the anonymous
`python () {}` block. An `addtask` line is recorded as a function named by
the task as written (`addtask check` is `check`, not `do_check`); the function
that implements the task is its own symbol.
