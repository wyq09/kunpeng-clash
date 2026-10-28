package qrdecode

import (
	"bytes"
	"encoding/base64"
	"image/png"
	"testing"

	"github.com/makiuchi-d/gozxing"
	"github.com/makiuchi-d/gozxing/qrcode"
)

func TestRoundTrip(t *testing.T) {
	want := "https://sub.example.com/api/v1/client/subscribe?token=abc"
	bm, err := qrcode.NewQRCodeWriter().Encode(want, gozxing.BarcodeFormat_QR_CODE, 300, 300, nil)
	if err != nil {
		t.Fatal(err)
	}
	var buf bytes.Buffer
	if err := png.Encode(&buf, bm); err != nil {
		t.Fatal(err)
	}
	got, err := FromBytes(buf.Bytes())
	if err != nil || got != want {
		t.Fatalf("FromBytes = %q, %v", got, err)
	}
	got, err = FromBase64("data:image/png;base64," + base64.StdEncoding.EncodeToString(buf.Bytes()))
	if err != nil || got != want {
		t.Fatalf("FromBase64 = %q, %v", got, err)
	}
	if _, err := FromBytes([]byte("not an image")); err == nil {
		t.Fatal("expected error for non-image")
	}
}
