package local

import (
	"fmt"
	"strings"
	"testing"
)

func TestNativeActivityTrace(t *testing.T) {
	trace := &nativeTrace{}
	for i := 1; i <= 40; i++ {
		s := fmt.Sprintf("NODEXLM_NATIVE_SPLIT batch=%d split=0 nodes=7 backend=CPU\nNODEXLM_NATIVE_SPLIT batch=%d split=1 nodes=8 backend=RPC0\nNODEXLM_NATIVE_BATCH batch=%d inputs=1 status=0 output=1\nNODEXLM_NATIVE_TOKEN batch=%d task=0 slot=0 ordinal=%d attributable=1\n", i, i, i, i, i+1)
		_, _ = trace.Write([]byte(s[:13]))
		_, _ = trace.Write([]byte(s[13:]))
	}
	got := trace.snapshot()
	if len(got) != nativeTraceLimit || got[0].Batch != 25 || got[15].Ordinal != 41 || len(got[15].Splits) != 2 || got[15].Splits[1].Backend != "RPC0" {
		t.Fatalf("unexpected history: %+v", got)
	}
	_, _ = trace.Write([]byte(strings.Repeat("x", 2048) + "\nNODEXLM_NATIVE_SPLIT batch=41 split=0 nodes=1 backend=CPU\nNODEXLM_NATIVE_BATCH batch=41 inputs=2 status=0 output=1\nNODEXLM_NATIVE_TOKEN batch=41 task=0 slot=0 ordinal=42 attributable=1\n"))
	_, _ = trace.Write([]byte("NODEXLM_NATIVE_SPLIT batch=42 split=0 nodes=1 backend=CPU\nNODEXLM_NATIVE_BATCH batch=42 inputs=1 status=0 output=1\nNODEXLM_NATIVE_TOKEN batch=42 task=0 slot=0 ordinal=43 attributable=0\n"))
	if last := trace.snapshot()[15]; last.Batch != 40 {
		t.Fatalf("non-attributable batch accepted: %+v", last)
	}
}
