// Package qrdecode 从图片（文件路径 / base64 / data URL）解出二维码文本（纯 Go，gozxing）。
package qrdecode

import (
	"bytes"
	"encoding/base64"
	"errors"
	"fmt"
	"image"
	_ "image/gif"
	_ "image/jpeg"
	_ "image/png"
	"os"
	"path/filepath"
	"strings"

	"github.com/makiuchi-d/gozxing"
	"github.com/makiuchi-d/gozxing/qrcode"
)

// FromPath 解码图片文件。
func FromPath(path string) (string, error) {
	if strings.HasPrefix(path, "~/") {
		if home, err := os.UserHomeDir(); err == nil {
			path = filepath.Join(home, path[2:])
		}
	}
	b, err := os.ReadFile(path)
	if err != nil {
		return "", fmt.Errorf("读取图片失败: %w", err)
	}
	return FromBytes(b)
}

// FromBase64 解码 base64 图片（可带 data:image/...;base64, 前缀）。
func FromBase64(s string) (string, error) {
	s = strings.TrimSpace(s)
	if i := strings.Index(s, ","); strings.HasPrefix(s, "data:") && i > 0 {
		s = s[i+1:]
	}
	b, err := base64.StdEncoding.DecodeString(s)
	if err != nil {
		if b, err = base64.RawStdEncoding.DecodeString(s); err != nil {
			return "", errors.New("图片 base64 解码失败")
		}
	}
	return FromBytes(b)
}

// FromBytes 解码图片字节（PNG / JPEG / GIF）。
func FromBytes(b []byte) (string, error) {
	img, _, err := image.Decode(bytes.NewReader(b))
	if err != nil {
		return "", errors.New("无法识别的图片格式（支持 PNG / JPEG / GIF）")
	}
	bmp, err := gozxing.NewBinaryBitmapFromImage(img)
	if err != nil {
		return "", fmt.Errorf("图片处理失败: %w", err)
	}
	hints := map[gozxing.DecodeHintType]interface{}{gozxing.DecodeHintType_TRY_HARDER: true}
	res, err := qrcode.NewQRCodeReader().Decode(bmp, hints)
	if err != nil {
		return "", errors.New("图片里没有识别到二维码（请确认截图清晰、二维码完整）")
	}
	text := strings.TrimSpace(res.GetText())
	if text == "" {
		return "", errors.New("二维码内容为空")
	}
	return text, nil
}
