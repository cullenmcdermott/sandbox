package terminal

import (
	"encoding/base64"
	"strings"
	"testing"

	"github.com/rivo/uniseg"
)

func TestKittyPlaceholderWidth(t *testing.T) {
	// A cols-wide single-row placement must measure exactly cols display columns
	// — the invariant that keeps the gauge from shifting the statusline.
	for _, cols := range []int{1, 5, 10, 20} {
		s := KittyPlaceholders(7, cols, 1)
		// Strip SGR so uniseg measures only the cells.
		clean := stripSGR(s)
		if w := uniseg.StringWidth(clean); w != cols {
			t.Errorf("cols=%d: width %d, want %d", cols, w, w-(w-cols))
		}
	}
}

func TestKittyPlaceholderEncoding(t *testing.T) {
	s := KittyPlaceholders(0x010203, 2, 1)
	if !strings.Contains(s, "\x1b[38;2;1;2;3m") {
		t.Errorf("expected fg color encoding id bytes, got %q", s)
	}
	if !strings.Contains(s, placeholder) {
		t.Error("expected U+10EEEE placeholder char")
	}
	if strings.Count(s, placeholder) != 2 {
		t.Errorf("expected 2 placeholder cells, got %d", strings.Count(s, placeholder))
	}
	if !strings.HasSuffix(s, "\x1b[39m") {
		t.Error("expected fg reset at end of row")
	}
}

func TestKittyPlaceholderDegenerate(t *testing.T) {
	if KittyPlaceholders(0, 5, 1) != "" {
		t.Error("id 0 must yield empty")
	}
	if KittyPlaceholders(1, 0, 1) != "" {
		t.Error("cols<1 must yield empty")
	}
	if KittyPlaceholders(1, 5, 0) != "" {
		t.Error("rows<1 must yield empty")
	}
}

func TestKittyTransmitStructure(t *testing.T) {
	rgba := GaugeRGBA(0.5, 10, 2, RGB{0, 255, 0}, RGB{40, 40, 40})
	s := KittyTransmitRGBA(42, 10, 1, 10, 2, rgba)
	if !strings.HasPrefix(s, apcStart) {
		t.Error("expected APC start")
	}
	if !strings.HasSuffix(s, apcEnd) {
		t.Error("expected APC end (ST)")
	}
	for _, want := range []string{"a=T", "U=1", "i=42", "f=32", "s=10", "v=2", "c=10", "r=1", "q=2"} {
		if !strings.Contains(s, want) {
			t.Errorf("transmission missing %q in %q", want, s[:min(len(s), 80)])
		}
	}
	// The payload after ';' must be valid base64 of the rgba bytes (single chunk
	// here since it's well under the 4096 limit).
	semi := strings.IndexByte(s, ';')
	payload := strings.TrimSuffix(s[semi+1:], apcEnd)
	dec, err := base64.StdEncoding.DecodeString(payload)
	if err != nil {
		t.Fatalf("payload not valid base64: %v", err)
	}
	if len(dec) != len(rgba) {
		t.Fatalf("decoded %d bytes, want %d", len(dec), len(rgba))
	}
}

func TestKittyTransmitChunking(t *testing.T) {
	// Force >1 chunk: a big image. width*height*4 base64 must exceed kittyChunkSize.
	rgba := GaugeRGBA(0.5, 200, 40, RGB{0, 255, 0}, RGB{0, 0, 0}) // 200*40*4 = 32000 bytes
	s := KittyTransmitRGBA(1, 10, 1, 200, 40, rgba)
	chunks := strings.Count(s, apcStart)
	if chunks < 2 {
		t.Fatalf("expected chunked transmission (>=2), got %d", chunks)
	}
	// Exactly one chunk should carry m=0 (the last); the rest m=1.
	if strings.Count(s, "m=0") != 1 {
		t.Errorf("expected exactly one final (m=0) chunk, got %d", strings.Count(s, "m=0"))
	}
	if strings.Count(s, "m=1") != chunks-1 {
		t.Errorf("expected %d continuation (m=1) chunks, got %d", chunks-1, strings.Count(s, "m=1"))
	}
}

func TestKittyTransmitDegenerate(t *testing.T) {
	if KittyTransmitRGBA(0, 10, 1, 10, 2, []byte{1, 2, 3, 4}) != "" {
		t.Error("id 0 must yield empty transmission")
	}
	if KittyTransmitRGBA(1, 10, 1, 10, 2, nil) != "" {
		t.Error("empty rgba must yield empty transmission")
	}
}

func TestGaugeRGBA(t *testing.T) {
	fill := RGB{0, 255, 0}
	empty := RGB{10, 10, 10}
	pix := GaugeRGBA(1.0, 4, 1, fill, empty)
	if len(pix) != 4*1*4 {
		t.Fatalf("len = %d, want 16", len(pix))
	}
	// frac=1 → every column is fully fill, opaque.
	for x := 0; x < 4; x++ {
		i := x * 4
		if pix[i] != 0 || pix[i+1] != 255 || pix[i+2] != 0 || pix[i+3] != 0xff {
			t.Fatalf("col %d = %v, want fill opaque", x, pix[i:i+4])
		}
	}
	// frac=0 → every column is empty.
	pix0 := GaugeRGBA(0, 4, 1, fill, empty)
	for x := 0; x < 4; x++ {
		i := x * 4
		if pix0[i] != 10 || pix0[i+1] != 10 || pix0[i+2] != 10 {
			t.Fatalf("frac=0 col %d = %v, want empty", x, pix0[i:i+3])
		}
	}
}

func TestGaugeRGBADegenerate(t *testing.T) {
	if GaugeRGBA(0.5, 0, 1, RGB{}, RGB{}) != nil {
		t.Error("zero width must yield nil")
	}
}

// stripSGR removes CSI SGR sequences (ESC [ … m) so width can be measured.
func stripSGR(s string) string {
	var b strings.Builder
	for i := 0; i < len(s); {
		if s[i] == '\x1b' && i+1 < len(s) && s[i+1] == '[' {
			j := i + 2
			for j < len(s) && s[j] != 'm' {
				j++
			}
			if j < len(s) {
				i = j + 1
				continue
			}
		}
		b.WriteByte(s[i])
		i++
	}
	return b.String()
}
