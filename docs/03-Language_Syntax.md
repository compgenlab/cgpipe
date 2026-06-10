# Language Syntax

A `.cgp` file is read top to bottom as cgp code. Every uncommented line at the top
level is a statement: a variable assignment, a control-flow block, a target
declaration, or a built-in like `print` or `include`. Inside a target's `{{ }}`,
the rules change — that text is a shell template, covered in
[Build Targets](05-Build_Targets.md). This chapter is the top-level language.

## The two kinds of braces

The single most important lexical rule in cgp:

- `{ ... }` delimits a block of **cgp code** — the body of an `if` or `for`.
- `{{ ... }}` delimits a **shell body** — a target's recipe, captured verbatim.

Keep them straight and the rest follows.

## Comments and help text

`#` begins a comment to end of line. The leading run of comment lines at the top
of a file (after an optional `#!` shebang) is the script's **help text**, printed
by `cgp script.cgp -h`:

```
#!/usr/bin/env cgp
#
# Align reads and call variants.
#
# Options:
#     --reads FILE   input FASTQ
#     --ref FILE     reference FASTA
```

The first blank or non-comment line ends the help block.

## Types

Six types. Typing is dynamic — you rarely name a type, and `.type()` returns it as
a string.

```
flag    = true            # bool   (lowercase true / false)
count   = 10              # int
rate    = 0.5             # float
name    = "sample-1"      # string (always double-quoted)
samples = ["s1", "s2"]    # list   (may mix types)
chunks  = 1..100          # range  (1, 2, … 100 when iterated)
```

A **range** stores only its bounds — never a materialized list — so `1..1000000`
costs nothing until you iterate it, yet it has `.length()`, indexes, and
`.contains()` like a list. Ranges are inclusive of both ends and may descend:

```
for x in 5..1 { print x }    # 5 4 3 2 1
print (1..10).contains(10)   # true
```

## Variables

No declarations; the only scopes are global and a per-target body closure. Four
forms:

| Form | Meaning |
|------|---------|
| `foo = expr`  | Set `foo` |
| `foo ?= expr` | Set `foo` **only if not already set** — the defaults workhorse |
| `foo += expr` | Append to `foo` (promotes a scalar to a list) |
| `unset foo`   | Remove `foo` |

```
threads = 4
threads ?= 16      # already set → stays 4
method  ?= "fast"  # unset → becomes "fast"

nums = 1
nums += 2          # scalar promoted to a list → 1 2
```

`?=` respects values set upstream (command line, env, config), which is what makes
it the right tool for defaults.

### Command-line variables

A **double-hyphen** `--name value` sets the script variable `name` before the
script runs. (Single-hyphen arguments like `-dr` are cgp's own options;
double-hyphen are always script variables.)

```sh
cgp pipeline.cgp --sample patient_42 --threads 16
```

Values are typed like literals (`16` → int, `0.5` → float, `true`/`false` → bool,
else string). Three conventions:

- **Boolean flag:** a bare `--adaptive` sets `adaptive = true`.
- **Hyphens → underscores:** `--hp-dist` sets `hp_dist` (identifiers can't contain
  hyphens).
- **Repeat → list:** `--x a --x b` gives `x = ["a", "b"]`.

Two edge cases: a value starting with `-` needs the explicit form
(`--offset=-5`); and put the pipeline file *before* a trailing boolean flag
(`cgp p.cgp --adaptive`) so the filename isn't swallowed as the flag's value.

Because CLI values are applied first, `?=` defaults never override them. Guard the
required ones:

```
if !reads { print "ERROR: --reads required"; exit 1 }
```

## Operators

```
print 1 + 2 * 3        # 7        standard precedence
print (1 + 2) * 3      # 9        grouping
print 7 / 2            # 3        int division
print 7.0 / 2          # 3.5      float
print 7 % 3            # 1        modulo
print 2 ** 10          # 1024     power (right-associative)
print "ab" + "cd"      # abcd     + concatenates strings
print "ab" * 3         # ababab   * repeats strings…
print [1, 2] * 2       # 1 2 1 2  …and lists
```

Comparison: `== != < <= > >=`. Logic: `&& || !`. The `!` operator doubles as an
"unset or empty/false" test — `!foo` is the idiom for argument guards
(`!""` is `true`, `!"x"` is `false`).

## Indexing and slicing

Zero-indexed; negative indices count from the end; slices are half-open and
clamp out-of-range:

```
f = ["a", "b", "c", "d"]
f[0]      # a
f[-1]     # d
f[1:3]    # b c
f[-2:]    # c d
f[:-1]    # a b c
f[10:]    # (empty)
```

## String substitution

Inside a `"…"` string literal:

| Form | Behavior |
|------|----------|
| `${var}`   | Substitute `var`; **error if unset**; a list joins with spaces |
| `${var?}`  | Like `${var}` but yields `""` when unset |
| `${expr}`  | Any expression: `${input[0]}`, `${name.basename()}` |
| `${if c; a; b}` | Inline conditional (see [Build Targets](05-Build_Targets.md)) |
| `@{list}`  | List expansion — one copy per element |
| `${{var}}` | Double evaluation — substitute, then evaluate the result as cgp source |
| `$(cmd)`   | Run `cmd` in the shell **at parse time**; substitute its stdout |

```
name = "sample-1"
print "hi ${name}"          # hi sample-1
print "missing=[${nope?}]"  # missing=[]
```

`${{var}}` is for when a variable's *content* is itself a template:

```
tmpl = "sample is ${name}"
print "${{tmpl}}"           # sample is sample-1
```

`$(cmd)` runs at parse time and its command string is substituted first:

```
print "$(echo ${name})"     # sample-1
```

> **`$(cmd)` runs even under `-dr`,** because rendering the script is what
> evaluates it. To defer a command to the *job's* shell at run time, write
> `\$(cmd)`. See [Troubleshooting](17-Troubleshooting.md).

### Escaping

Inside a string literal a backslash escapes the next character: `\$` and `\@`
produce a literal `$`/`@` (suppressing substitution), and `\"` a literal quote.

```
name = "bob"
print "${name} vs \${name}"   # bob vs ${name}
print "cost is \$5"           # cost is $5
print "Hello \"world\"!"      # Hello "world"!
```

A string is one escape domain, resolved before the `${…}` interior is parsed, so a
nested string argument escapes its quotes to survive the outer ones:

```
print "stem=${name.sub(\".bam\", \"\")}"   # works
```

(A string two `${…}` layers deep needs `\\\\` to land a single backslash in the
innermost string — rare, but see [§4.3 of the spec](language-spec.md#43-string-substitution).)

## Control flow

`{ }` blocks; `if`/`elif`/`else`, `for…in`, and a while-style single-condition
`for`:

```
if count > 100 {
    print "many"
} elif count > 0 {
    print "some"
} else {
    print "none"
}

for i in 1..3        { print "range", i }
for s in ["a", "b"]  { print "list", s }

n = 0
for n < 3 {
    print "while", n
    n = n + 1
}
```

Add `with <name>` to a `for…in` to bind a **1-based** loop counter alongside the
element — handy for numbering iterations (e.g. [array task ids](09-Array_Jobs.md)):

```
for s in ["a", "b", "c"] with i { print i, s }   # 1 a / 2 b / 3 c
```

Loop variables remain set after the loop (no separate scope) — the counter too.

## Statements

| Statement | Purpose |
|-----------|---------|
| `print expr [, expr …]` | Write to stdout (comma-separated args, space-joined). Inside a body, appends to the rendered script instead. |
| `include "path"` | Inline another `.cgp` file in global context — the composition mechanism (see [Tutorial 8](tutorials/08-include.md)) |
| `export name = expr` | Expose a value to a calling [workflow](12-Workflows.md); a no-op standalone |
| `eval expr` | Evaluate a string-valued expression as cgp source at run time |
| `unset name` | Remove a variable |
| `exit [code]` | Stop the pipeline (`exit` ⇒ `exit 0`); the code becomes cgp's exit status |
| `dumpvars` | Print all in-scope variables (debug) |
| `showhelp` | Print the help-text block |

```
eval "answer = 6 * 7"
print answer        # 42
```

## Next

- **[Methods Reference](04-Methods_Reference.md)** — what you can call on each type.
- **[Build Targets](05-Build_Targets.md)** — turn variables and loops into rules.

Reference → [language-spec.md §1–§5](language-spec.md#1-lexical-structure).
