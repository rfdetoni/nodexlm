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

func TestNativeActivityDecodeDuration(t *testing.T) {
	trace := &nativeTrace{}
	for i, suffix := range []string{" decode_us=1234", "", " decode_us=-1", " decode_us=0"} {
		batch := i + 1
		s := fmt.Sprintf("NODEXLM_NATIVE_SPLIT batch=%d split=0 nodes=7 backend=CPU\nNODEXLM_NATIVE_BATCH batch=%d inputs=1 status=0 output=1%s\nNODEXLM_NATIVE_TOKEN batch=%d task=0 slot=0 ordinal=2 attributable=1\n", batch, batch, suffix, batch)
		_, _ = trace.Write([]byte(s))
	}
	got := trace.snapshot()
	if len(got) != 3 || got[0].DecodeMicroseconds == nil || *got[0].DecodeMicroseconds != 1234 ||
		got[1].DecodeMicroseconds != nil || got[2].Batch != 4 || got[2].DecodeMicroseconds == nil || *got[2].DecodeMicroseconds != 0 {
		t.Fatalf("unexpected native duration: %+v", got)
	}
	*got[0].DecodeMicroseconds = 9
	if *trace.snapshot()[0].DecodeMicroseconds != 1234 {
		t.Fatal("snapshot mutated trace")
	}
}

func TestNativeActivityRedactsPrivateRelayAndPreservesBasis(t *testing.T) {
	trace := &nativeTrace{}
	_, _ = trace.Write([]byte("NODEXLM_NATIVE_SPLIT batch=1 split=0 nodes=3 backend=unix:/tmp/nodexlm-relay-123/rpc.sock\n" +
		"NODEXLM_NATIVE_BATCH batch=1 inputs=1 status=0 output=1 decode_us=500\n" +
		"NODEXLM_NATIVE_TOKEN batch=1 task=0 slot=2 ordinal=2 attributable=1\n"))
	got := trace.snapshot()
	if len(got) != 1 || got[0].Splits[0].Backend != "unknown/unattributed" ||
		got[0].SlotID != 2 || got[0].ProcessEpoch == "" || got[0].MeasurementBasis != nativeMeasurementBasis {
		t.Fatalf("unexpected sanitized activity: %+v", got)
	}
	m := &Managed{}
	if act := m.NativeActivity(); act != nil {
		t.Fatalf("expected nil NativeActivity when tracing disabled, got %+v", act)
	}
}
