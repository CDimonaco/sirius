// Command sirius bridges meeting audio on this machine to a physical phone.
//
// This iteration has no user interface. It waits for the phone to register, rings it
// once, bridges the audio until someone hangs up, and prints what it counted. The tray
// comes later; everything below it is already the real thing.
package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"log"
	"net"
	"os"
	"os/signal"
	"time"

	"github.com/CDimonaco/sirius/internal/audio"
	"github.com/CDimonaco/sirius/internal/bridge"
	"github.com/CDimonaco/sirius/internal/sipsrv"
)

func main() {
	var (
		listDevices = flag.Bool("devices", false, "list host audio devices and exit")
		account     = flag.String("account", "phone", "SIP user the phone registers as")
		password    = flag.String("password", "", "SIP password the phone registers with")
		realm       = flag.String("realm", "sirius", "digest realm offered to the phone")
		bind        = flag.String("bind", "", "address the phone can reach us on, empty means this machine's LAN address")
		port        = flag.Int("port", 5060, "SIP port")
		capture     = flag.String("capture", "BlackHole 2ch", "device a meeting plays into, matched by substring")
		playback    = flag.String("playback", "BlackHole 16ch", "device a meeting records from, matched by substring")
		callerID    = flag.String("caller-id", "Sirius", "display name the phone shows")
	)
	flag.Parse()

	if err := run(*listDevices, sipsrv.Account{User: *account, Password: *password, Realm: *realm},
		*bind, *port, *capture, *playback, *callerID); err != nil {
		log.Fatal(err)
	}
}

func run(listDevices bool, account sipsrv.Account, bind string, port int, capture, playback, callerID string) error {
	ctx, cancel := signal.NotifyContext(context.Background(), os.Interrupt)
	defer cancel()

	host, err := audio.NewHost()
	if err != nil {
		return err
	}
	defer func() {
		if err := host.Close(); err != nil {
			log.Printf("closing audio: %v", err)
		}
	}()

	if listDevices {
		return printDevices(host)
	}
	if account.Password == "" {
		return errors.New("-password is required: an open registrar would let anyone on this network receive your meeting audio")
	}
	if bind == "" {
		if bind, err = lanAddress(); err != nil {
			return err
		}
	}

	srv, err := sipsrv.NewServer(account, bind, port)
	if err != nil {
		return err
	}
	if err := srv.Serve(ctx); err != nil {
		return fmt.Errorf("listen on %s: %w", srv.Address(), err)
	}
	log.Printf("listening on %s, point the phone at it as user %q", srv.Address(), account.User)

	if err := waitForPhone(ctx, srv); err != nil {
		return err
	}

	call, err := srv.Ring(ctx, callerID)
	if err != nil {
		return err
	}
	defer func() {
		if err := call.Hangup(); err != nil {
			log.Printf("hanging up: %v", err)
		}
	}()
	log.Printf("answered, codec %s, bridging %q to %q", call.Codec, capture, playback)

	var counters bridge.Counters
	err = bridge.Run(ctx, call,
		func(r *audio.Ring) (bridge.Device, error) { return host.Capture(capture, r) },
		func(r *audio.Ring) (bridge.Device, error) { return host.Playback(playback, r) },
		&counters)
	log.Printf("call ended: %s", counters.String())
	if err != nil && !errors.Is(err, context.Canceled) {
		return err
	}
	return nil
}

// waitForPhone blocks until a phone has registered. Polling keeps the registrar free of
// any notion of who is waiting for it.
func waitForPhone(ctx context.Context, srv *sipsrv.Server) error {
	log.Print("waiting for the phone to register")
	for {
		if _, ok := srv.Registrar.Contact(); ok {
			return nil
		}
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-time.After(time.Second):
		}
	}
}

func printDevices(host *audio.Host) error {
	capture, playback, err := host.Names()
	if err != nil {
		return err
	}
	fmt.Println("capture:")
	for _, n := range capture {
		fmt.Printf("  %s\n", n)
	}
	fmt.Println("playback:")
	for _, n := range playback {
		fmt.Printf("  %s\n", n)
	}
	return nil
}

// lanAddress finds the first non-loopback IPv4 address, which is the one a phone on the
// same network can reach. It also goes in the SDP as the media address, so 127.0.0.1
// would leave the phone with nowhere to send audio.
func lanAddress() (string, error) {
	addrs, err := net.InterfaceAddrs()
	if err != nil {
		return "", fmt.Errorf("read interface addresses: %w", err)
	}
	for _, a := range addrs {
		ipnet, ok := a.(*net.IPNet)
		if !ok || ipnet.IP.IsLoopback() {
			continue
		}
		if v4 := ipnet.IP.To4(); v4 != nil {
			return v4.String(), nil
		}
	}
	return "", errors.New("no non-loopback IPv4 address found, pass -bind")
}
