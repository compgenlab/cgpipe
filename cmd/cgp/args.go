package main

// argCursor walks a flag list, factoring out the "option that needs a value"
// bookkeeping shared by the sub / convert / ledger subcommand parsers. The
// caller still owns dispatch and usage text; this only removes the repeated
// index-juggling and the "needs a value" boilerplate.
type argCursor struct {
	args []string
	i    int
}

func newArgCursor(args []string) *argCursor { return &argCursor{args: args} }

// more reports whether there is a current token to read.
func (c *argCursor) more() bool { return c.i < len(c.args) }

// cur returns the current token (caller must check more() first).
func (c *argCursor) cur() string { return c.args[c.i] }

// advance steps past the current token.
func (c *argCursor) advance() { c.i++ }

// value consumes the token following the current flag and returns it, advancing
// past both. ok is false when no value follows (the cursor is left unmoved so
// the caller can report its own "needs a value" usage).
func (c *argCursor) value() (string, bool) {
	if c.i+1 >= len(c.args) {
		return "", false
	}
	c.i++
	v := c.args[c.i]
	c.i++
	return v, true
}
