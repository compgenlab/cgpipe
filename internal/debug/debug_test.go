package debug

import (
	"bytes"
	"strings"
	"testing"
)

func TestLevelGatingAndFormat(t *testing.T) {
	var buf bytes.Buffer
	prev := SetWriter(&buf)
	defer SetWriter(prev)
	defer SetLevel(0)

	// Level 0: nothing is emitted.
	SetLevel(0)
	Logf(1, "suppressed")
	if buf.Len() != 0 {
		t.Fatalf("level 0 emitted %q", buf.String())
	}

	// Level 2: On() gates correctly, and only n<=2 lines print, prefixed cgpipe[dN].
	SetLevel(2)
	if !On(1) || !On(2) || On(3) {
		t.Fatalf("On() wrong at level 2: On(1)=%v On(2)=%v On(3)=%v", On(1), On(2), On(3))
	}
	Logf(1, "one %d", 7)
	Logf(2, "two")
	Logf(3, "three")
	out := buf.String()
	if !strings.Contains(out, "cgpipe[d1] one 7\n") || !strings.Contains(out, "cgpipe[d2] two\n") {
		t.Fatalf("missing expected lines:\n%s", out)
	}
	if strings.Contains(out, "three") {
		t.Fatalf("level 3 leaked at level 2:\n%s", out)
	}
}
