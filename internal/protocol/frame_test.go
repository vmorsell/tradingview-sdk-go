package protocol

import (
	"bytes"
	"encoding/json"
	"strings"
	"testing"
)

func TestEncodeDecodeRoundtrip(t *testing.T) {
	frame, err := Encode("quote_create_session", "qs_abc")
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.HasPrefix(frame, []byte("~m~")) {
		t.Fatalf("frame missing prefix: %q", frame)
	}

	envs, err := Decode(frame)
	if err != nil {
		t.Fatalf("Decode: %v", err)
	}
	if len(envs) != 1 {
		t.Fatalf("want 1 envelope, got %d", len(envs))
	}
	if envs[0].Method != "quote_create_session" {
		t.Fatalf("wrong method: %q", envs[0].Method)
	}
	if got := envs[0].SessionID(); got != "qs_abc" {
		t.Fatalf("wrong session: %q", got)
	}
}

func TestDecodeMultiFrame(t *testing.T) {
	a, _ := Encode("a", "1")
	b, _ := Encode("b", "2")
	envs, err := Decode(append(append([]byte{}, a...), b...))
	if err != nil {
		t.Fatal(err)
	}
	if len(envs) != 2 || envs[0].Method != "a" || envs[1].Method != "b" {
		t.Fatalf("bad split: %+v", envs)
	}
}

func TestDecodeHeartbeat(t *testing.T) {
	envs, err := Decode([]byte("~m~4~m~~h~7"))
	if err != nil {
		t.Fatal(err)
	}
	if len(envs) != 1 || !envs[0].IsPing() || envs[0].Ping != 7 {
		t.Fatalf("bad heartbeat: %+v", envs)
	}
}

func TestDecodeBareNumberPing(t *testing.T) {
	envs, err := Decode([]byte("~m~3~m~123"))
	if err != nil {
		t.Fatal(err)
	}
	if len(envs) != 1 || !envs[0].IsPing() || envs[0].Ping != 123 {
		t.Fatalf("bad bare ping: %+v", envs)
	}
}

func TestDecodeSessionHelloHasNoEnvelope(t *testing.T) {
	// Server sends plain JSON (no m/p) on first connect.
	body := `{"session_id":"<0.1.0>","timestamp":1,"release":"r"}`
	frame := []byte("~m~" + itoa(len(body)) + "~m~" + body)
	envs, err := Decode(frame)
	if err != nil {
		t.Fatalf("Decode: %v", err)
	}
	if len(envs) != 1 || envs[0].Method != "" || len(envs[0].Raw) == 0 {
		t.Fatalf("want raw hello, got %+v", envs[0])
	}
	var m map[string]any
	if err := json.Unmarshal(envs[0].Raw, &m); err != nil {
		t.Fatalf("raw not JSON: %v", err)
	}
}

func TestDecodePayloadContainingMarker(t *testing.T) {
	// The length prefix is authoritative; a literal "~m~" inside the payload
	// must not confuse the splitter.
	payload := `{"m":"x","p":["~m~99~m~sneaky"]}`
	frame := []byte("~m~" + itoa(len(payload)) + "~m~" + payload)
	envs, err := Decode(frame)
	if err != nil {
		t.Fatalf("Decode: %v", err)
	}
	if len(envs) != 1 || envs[0].Method != "x" {
		t.Fatalf("got %+v", envs)
	}
	if envs[0].SessionID() != "~m~99~m~sneaky" {
		t.Fatalf("bad params[0]: %q", envs[0].SessionID())
	}
}

func TestDecodeTruncated(t *testing.T) {
	if _, err := Decode([]byte("~m~10~m~abc")); err == nil {
		t.Fatal("want error on short payload")
	}
	if _, err := Decode([]byte("~m~abc~m~x")); err == nil {
		t.Fatal("want error on non-numeric length")
	}
	if _, err := Decode([]byte("garbage")); err == nil {
		t.Fatal("want error on missing prefix")
	}
}

func TestEncodeHeartbeatFrame(t *testing.T) {
	got := string(EncodeHeartbeat(42))
	want := "~m~5~m~~h~42"
	if got != want {
		t.Fatalf("got %q want %q", got, want)
	}
}

func TestEncodeEmptyParams(t *testing.T) {
	frame, err := Encode("ping")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(frame), `"p":[]`) {
		t.Fatalf("empty params should serialize as []: %q", frame)
	}
}

func FuzzDecode(f *testing.F) {
	f.Add([]byte("~m~4~m~~h~7"))
	f.Add([]byte(`~m~30~m~{"m":"ok","p":["qs_abc",1]}`))
	f.Add([]byte(""))
	f.Add([]byte("~m~0~m~"))
	f.Fuzz(func(t *testing.T, data []byte) {
		// Only invariant: never panic.
		_, _ = Decode(data)
	})
}

// itoa is a tiny helper to avoid importing strconv in tests.
func itoa(n int) string {
	if n == 0 {
		return "0"
	}
	var buf [20]byte
	i := len(buf)
	for n > 0 {
		i--
		buf[i] = byte('0' + n%10)
		n /= 10
	}
	return string(buf[i:])
}
