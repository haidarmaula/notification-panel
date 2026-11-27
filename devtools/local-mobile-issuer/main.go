// Command local-mobile-issuer runs a local issuer for a physical-device
// smoke test. It is not a production identity or platform-attestation service.
package main

import (
	"errors"
	"flag"
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

func main() {
	if err := run(os.Args[1:]); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(2)
	}
}

func run(args []string) error {
	if len(args) == 0 {
		return errors.New("usage: local-mobile-issuer <init|serve> [options]")
	}
	switch args[0] {
	case "init":
		return initialize(args[1:])
	case "serve":
		return serve(args[1:])
	case "-h", "--help", "help":
		fmt.Fprintln(os.Stdout, "Usage: local-mobile-issuer <init|serve> [options]")
		return nil
	default:
		return errors.New("unknown command")
	}
}

func initialize(args []string) error {
	flags := flag.NewFlagSet("init", flag.ContinueOnError)
	directory := flags.String("dir", "", "new private directory for local issuer files")
	appID := flags.String("app-id", "", "existing active backend application UUID")
	namespace := flags.String("namespace", "hello-local-mobile-test", "mapped identity namespace")
	subject := flags.String("subject", "", "existing test subject mapped to an active application user")
	providerNamespace := flags.String("provider-namespace", "hello-local-fcm", "provider address namespace shared with worker policy")
	providerRouteKey := flags.String("provider-route-key", "android-fid", "provider route key shared with worker policy")
	if err := flags.Parse(args); err != nil {
		return errors.New("invalid init options")
	}
	if flags.NArg() != 0 || *directory == "" || !validUUID(*appID) ||
		!validIdentityText(*namespace) || !validIdentityText(*subject) ||
		!validIdentityText(*providerNamespace) || !validIdentityText(*providerRouteKey) {
		return errors.New("init requires --dir, --app-id, and a mapped --subject")
	}
	if err := createFiles(*directory, *appID, *namespace, *subject, *providerNamespace, *providerRouteKey); err != nil {
		return errors.New("local issuer files could not be created")
	}
	fmt.Fprintln(os.Stdout, "Created private issuer and mobile-policy files.")
	fmt.Fprintln(os.Stdout, "The application must already be ACTIVE and the subject must already be mapped.")
	fmt.Fprintln(os.Stdout, "Keep both files outside the repository. Match the worker route and address-protection keys to mobile-policy.json.")
	return nil
}

func serve(args []string) error {
	flags := flag.NewFlagSet("serve", flag.ContinueOnError)
	configPath := flags.String("config", "", "private issuer configuration file")
	listenAddress := flags.String("listen", "127.0.0.1:8081", "loopback listen address")
	qrAPI := flags.String("qr-api", "", "loopback backend origin to encode in a pairing QR")
	lan := flags.String("lan", "", "private IPv4 address and port for HTTPS pairing")
	lanTLS := flags.String("lan-tls-dir", "", "private local certificate directory")
	control := flags.String("control-socket", "", "private local dashboard control socket")
	publicOrigin := flags.String("public-origin", "", "HTTPS DNS origin encoded in public-variant pairing QRs")
	if err := flags.Parse(args); err != nil {
		return errors.New("invalid serve options")
	}
	if flags.NArg() != 0 || *configPath == "" || !loopbackAddress(*listenAddress) {
		return errors.New("serve requires --config and a loopback --listen address")
	}
	if *qrAPI != "" && !loopbackHTTPOrigin(*qrAPI) {
		return errors.New("--qr-api requires a loopback HTTP origin with a port")
	}
	if (*lan == "") != (*lanTLS == "") || (*lan != "" && *qrAPI == "") {
		return errors.New("LAN pairing requires --lan, --lan-tls-dir and --qr-api")
	}
	if *control != "" && !filepath.IsAbs(*control) {
		return errors.New("--control-socket requires an absolute path")
	}
	if *publicOrigin != "" && *lan == "" && *control == "" {
		return errors.New("--public-origin requires a LAN issuer or a control socket")
	}
	cfg, err := readConfig(*configPath)
	if err != nil {
		return errors.New("local issuer configuration rejected")
	}
	if *lan != "" {
		return runLANServer(cfg, *lan, *lanTLS, *qrAPI, *control, *publicOrigin)
	}
	if *control != "" {
		if err := runPairingServer(cfg, *listenAddress, *qrAPI, nil, *control, *publicOrigin); err != nil {
			return errors.New("local issuer stopped")
		}
		return nil
	}
	if err := runServer(cfg, *listenAddress, *qrAPI); err != nil {
		return errors.New("local issuer stopped")
	}
	return nil
}

func absoluteDirectory(path string) (string, error) {
	clean, err := filepath.Abs(filepath.Clean(strings.TrimSpace(path)))
	if err != nil || clean == string(filepath.Separator) {
		return "", errors.New("invalid directory")
	}
	return clean, nil
}
