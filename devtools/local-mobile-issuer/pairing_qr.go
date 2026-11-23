package main

import (
	"fmt"
	"io"
	"net/url"
	"strconv"
	"strings"

	"github.com/skip2/go-qrcode"
)

func loopbackHTTPOrigin(value string) bool {
	origin, err := url.Parse(value)
	if err != nil || origin.Scheme != "http" || origin.User != nil || !loopbackAddress(origin.Host) {
		return false
	}
	port, err := strconv.ParseUint(origin.Port(), 10, 16)
	return err == nil && port != 0 && value == "http://"+origin.Host
}

func pairingLink(api, issuer, appID, code string) string {
	parameters := url.Values{
		"api":    {api},
		"issuer": {issuer},
		"app":    {appID},
		"code":   {code},
	}
	link := url.URL{Scheme: "hellofcmsmoke", Host: "pair", RawQuery: parameters.Encode()}
	return link.String()
}

func writePairingQR(output io.Writer, link string) error {
	qr, err := qrcode.New(link, qrcode.Medium)
	if err != nil {
		return err
	}
	if _, err := fmt.Fprintln(output, "Scan this one-use QR in the smoke app."); err != nil {
		return err
	}
	for row := range strings.SplitSeq(strings.TrimSuffix(qr.ToSmallString(true), "\n"), "\n") {
		if _, err := fmt.Fprintf(output, "\x1b[30;107m%s\x1b[0m\n", row); err != nil {
			return err
		}
	}
	return nil
}
