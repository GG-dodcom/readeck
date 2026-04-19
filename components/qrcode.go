// SPDX-FileCopyrightText: © 2026 Olivier Meunier <olivier@neokraft.net>
//
// SPDX-License-Identifier: AGPL-3.0-only

package components

import (
	"encoding/base64"
	"image/color"
	"strings"

	"github.com/skip2/go-qrcode"
)

// ColorPrimary is blue-800.
var ColorPrimary = color.RGBA{6, 76, 92, 255}

// QRCodeB64 returns a QR code as a base64 data URI.
// It panics when src is too big.
// Size can be a negative number. See [qrcode.QRCode.Image]
// for details. A value of -6 is a good default for URLs.
func QRCodeB64(src string, color color.Color, size int) string {
	qr, err := qrcode.New(src, qrcode.Medium)
	if err != nil {
		panic(err)
	}
	qr.ForegroundColor = color
	qr.DisableBorder = true

	data, err := qr.PNG(size)
	if err != nil {
		panic(err)
	}

	res := new(strings.Builder)
	res.WriteString("data:image/png;base64,")
	enc := base64.NewEncoder(base64.StdEncoding, res)
	enc.Write(data) //nolint:errcheck

	return res.String()
}
