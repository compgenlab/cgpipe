# Methods Reference

Methods use dot syntax on a variable, a literal, or a chained result:
`"Mixed".lower().upper()`. Argument counts are checked at run time; an unknown
method throws `Method not found`. There is no implicit type coercion.

Every example below is taken from a verified fixture under
[`tests/lang/`](../tests/lang/).

## Any type

| Method | Returns | Description |
|--------|---------|-------------|
| `type()` | string | The type name: `"string"`, `"int"`, `"list"`, `"range"`, … |

```
print (1..5).type()    # range
print "x".type()       # string
```

## string

| Method | Args | Returns | Description |
|--------|------|---------|-------------|
| `split(delim)` | string, optional | list | Split on `delim`; **omitted ⇒ individual characters** |
| `sub(pattern, repl)` | string, string | string | Regex replace-all (Go RE2 syntax) |
| `upper()` / `lower()` | — | string | Case conversion |
| `length()` | — | int | Character count |
| `contains(s)` | string | bool | Substring test |
| `join(list)` | list | string | Receiver is the separator |
| `basename()` | — | string | `/data/reads/x.bam` → `x.bam` |
| `dirname()` | — | string | `/data/reads/x.bam` → `/data/reads` |
| `abspath()` | — | string | Resolve to an absolute path |
| `exists()` / `isfile()` / `isdir()` | — | bool | Filesystem test, at evaluation time |

```
name = "sample-1"
print name.upper()                 # SAMPLE-1
print name.length()                # 8
print name.contains("amp")         # true
print name.split("-")              # sample 1
print ",".join(["a", "b", "c"])    # a,b,c
print "/data/reads/x.bam".basename()   # x.bam
print "/data/reads/x.bam".dirname()    # /data/reads
```

### Regex with `sub`

`sub` uses Go's `regexp` (RE2). The pattern is a cgp string literal, so backslashes
are escaped: a literal `.` is `\\.`. Capture groups are `$1`, `$2`, …:

```
print "reads.bam".sub("\\.bam$", "")                          # reads
print "chr1:100-200".sub("chr(\\d+):(\\d+)-(\\d+)", "$1 $2 $3")  # 1 100 200
print "  trim me  ".sub("^\\s+|\\s+$", "")                    # trim me
```

### Splitting into characters

`split()` with no delimiter returns individual characters:

```
print "abc".split()    # a b c
```

Methods chain left to right:

```
print "/a/b/c.bam".basename().sub("\\.bam$", "")   # c
```

## list

| Method | Args | Returns |
|--------|------|---------|
| `length()` | — | int |
| `contains(value)` | any | bool |
| `join(separator)` | string | string |

Lists are also indexed, sliced, and appended with `+=` (see
[Language Syntax](03-Language_Syntax.md#indexing-and-slicing)).

```
foo = ["one", "two", "three"]
print foo.length()         # 3
print foo.contains("two")  # true
print foo.join(",")        # one,two,three
```

`",".join(list)` (receiver-flipped) is equivalent to `list.join(",")` — both work.

## range

| Method | Returns | Description |
|--------|---------|-------------|
| `length()` | int | Number of values |
| `contains(value)` | bool | Membership (bounds are inclusive) |

Ranges iterate, index, and pass anywhere a list is accepted — without ever
building the list:

```
big = 1..1000000
print big.type()      # range
print big.length()    # 1000000   (no million-element list is materialized)
```

## map

An ordered, string-keyed collection — the `{}` literal and the value
`read_tsv()`/`read_json()` produce per row ([Sample Sheets](13-Sample_Sheets.md)).

| Method | Args | Returns | Description |
|--------|------|---------|-------------|
| `get(key)` | string or int | any | Value for `key` (or i-th by position); missing ⇒ unset |
| `has(key)` | string | bool | Is `key` present |
| `keys()` | — | list | Keys, in insertion/column order |
| `values()` | — | list | Values, in key order |
| `items()` | — | list | One `[key, value]` pair per entry |
| `length()` | — | int | Number of entries |

Also read/written by index — `m["k"]`, `m[0]` (positional), `m["k"] = v`,
`m["k"] += v`. A field read keeps its type, so it chains:

```
row = open("samples.tsv").read_tsv()[0]
print row["sample"]              # by name
print row[0]                     # by position
print row["bam"].basename()      # chains a string method
```

## file

A handle from `open(path[, mode])` — `mode` is `"r"` (default), `"w"` (truncate), or
`"a"` (append). Read methods need an `"r"` handle; write methods a `"w"`/`"a"` one.

| Method | Args | Returns | Description |
|--------|------|---------|-------------|
| `read_tsv(...)` | kw: `header=true`, `sep="\t"`, `comment="#"`, `skip=0`, `raw=false` | list of map | Tab-delimited rows |
| `read_csv(...)` | kw: same, `sep=","` | list of map | Comma-delimited rows |
| `read_json()` | — | list of map | A JSON array of objects |
| `read_lines(...)` | kw: `comment=""`, `skip=0`, `blank=true` | list of string | Raw lines |
| `read()` | — | string | The whole file |
| `write(s)` / `writeln(s)` | any | file | Write `s` (verbatim / with a trailing newline) |
| `close()` | — | — | Flush and close (idempotent) |
| `exists()` / `path()` | — | bool / string | Introspection |

```
samples = open("samples.tsv").read_tsv(header=true)   # list of maps
ids     = open("ids.txt").read_lines(comment="#")     # list of strings

f = open("params.txt", "w")                           # writes run at eval time
f.writeln("ref=hg38")                                 # writeln adds the newline
f.close()                                             # (no-op under -dr, with a warning)
```

## int / float / bool

Only `type()`. Arithmetic and comparison are done with operators (see
[Language Syntax](03-Language_Syntax.md#operators)), not methods.

## Next

- **[Build Targets](05-Build_Targets.md)** — put these to work in rules.

Reference → [language-spec.md §9](language-spec.md#9-methods-on-built-in-types).
