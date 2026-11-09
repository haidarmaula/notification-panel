// Command local-mobile-issuer runs a loopback-only issuer for a physical-device
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
	if err := flags.Parse(args); err != nil {
		return errors.New("invalid serve options")
	}
	if flags.NArg() != 0 || *configPath == "" || !loopbackAddress(*listenAddress) {
		return errors.New("serve requires --config and a loopback --listen address")
	}
	cfg, err := readConfig(*configPath)
	if err != nil {
		return errors.New("local issuer configuration rejected")
	}
	if err := runServer(cfg, *listenAddress); err != nil {
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
