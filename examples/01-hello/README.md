# 01 — Hello

The smallest useful pipeline: one target that builds one file, a command-line
variable with a default, and a help block.

```sh
cgpipe pipeline.cgp | bash          # build hello.txt
cat hello.txt                    # Hello, world!

cgpipe pipeline.cgp --name cgpipe | bash
cat hello.txt                    # Hello, cgpipe!

cgpipe pipeline.cgp -h              # show the help text
```

Concepts: a `output: {{ body }}` target, `${output}`, `name ?= "world"` defaults,
`@default`, and the help-text comment block. cgpipe only rebuilds when the output is
missing or out of date — run it twice and the second run does nothing.

See [Tutorial 1](../../docs/tutorials/01-hello.md).
