![Sirius](docs/assets/banner.svg)

[![ci](https://github.com/CDimonaco/sirius/actions/workflows/ci.yml/badge.svg)](https://github.com/CDimonaco/sirius/actions/workflows/ci.yml)

Sirius takes the audio of whatever is running on your computer, Google Meet in a
browser, Zoom, a Slack huddle, and bridges it to a VoIP handset or to an analog phone
behind an ATA.

It installs nothing into Slack, Meet or Zoom. No API token, no bot, no admin
approval. Sirius runs as a small SIP registrar on your own machine, and the meeting
client only ever sees an ordinary system microphone and speaker.

## Status

The audio works. A phone registers, Sirius rings it with a caller ID of your choosing,
and audio crosses in both directions until someone hangs up. Measured against a
softphone on a separate device over wifi, a ten minute call showed no clock drift in
either direction.

There is no user interface yet, so this is a command you run and a device swap you make
by hand. Still missing: the tray, resolving the meeting name automatically, switching
the host's default devices, DTMF control from the handset, calendar, G.722, and Linux.

## Running it on macOS

### Once, to set the machine up

Sirius needs two virtual audio devices. One stands in for a speaker, so a meeting can
play into it; the other stands in for a microphone, so a meeting can record from it.

```sh
brew install --cask blackhole-2ch blackhole-16ch
sudo killall coreaudiod
```

The restart is not optional. macOS loads audio drivers only when `coreaudiod` starts,
so without it the devices stay invisible and the install looks broken.

Check that both appear, as input and as output:

```sh
make build
./bin/sirius -devices
```

Give the machine a fixed address on your network, through a DHCP reservation on the
router. The phone needs Sirius' address, few phones speak mDNS, and a registration
pointed at an address that has moved is a phone that never rings.

### Once, on the phone

Any SIP phone or softphone works. Create an account by hand rather than letting the app
search for a provider, and set:

| Setting | Value |
|---|---|
| Host or domain | the address Sirius prints when it starts, including the port |
| Username | `phone`, or whatever you pass to `-account` |
| Password | whatever you pass to `-password` |
| Transport | UDP |
| Codec | G.711 u-law, also written PCMU, and nothing else |
| STUN, outbound proxy, SRTP | off |

On iOS, keep the app in the foreground. Backgrounded, it stops answering.

### Every call

Start Sirius:

```sh
./bin/sirius -password yourpassword -caller-id "Standup"
```

It prints the address to point the phone at, waits for the phone to register, then rings
it with `Standup` on the display. Answer it.

Now send the meeting's audio into the bridge. In System Settings, Sound, set the output
to **BlackHole 2ch**. Your Mac's speakers go quiet, which is expected: that audio is
going to the handset instead. In the meeting client, choose **BlackHole 16ch** as the
microphone, and set Zoom to *Same as System* if you use it, because Zoom remembers a
pinned device and will ignore the change otherwise.

Hang up from the phone, or press Ctrl-C. Sirius prints what it counted:

```
call ended: to phone 3115 packets, from phone 2971 packets, inserted 0ms of silence,
dropped 0ms, 235 gaps totalling 25759ms, 0 silence pauses totalling 0ms
```

Read the inserted and dropped milliseconds first, because they say whether audio was
actually lost. The gap count is measured between Sirius' own reads, so on wifi it counts
bursty delivery as well as real trouble: many gaps with nothing inserted means the buffer
absorbed them.

### Options

```
-account    SIP user the phone registers as (default "phone")
-password   SIP password, required
-realm      digest realm offered to the phone (default "sirius")
-bind       address the phone can reach, defaults to this machine's LAN address
-port       SIP port (default 5060)
-capture    device a meeting plays into (default "BlackHole 2ch")
-playback   device a meeting records from (default "BlackHole 16ch")
-caller-id  display name the phone shows (default "Sirius")
-devices    list host audio devices and exit
```

## How it works

[docs/architecture/overview.md](docs/architecture/overview.md) has the design and the
risks that are still open. Behaviour is specified under [`openspec/specs/`](openspec/specs/).

## Development

Requires [asdf](https://asdf-vm.com). The Go version is pinned in `.tool-versions`.

```sh
asdf install
make build
make test
make lint
```

No test needs an audio device, a phone or a meeting.

macOS is the first target, Linux the second.

## The name

The Sirio was the desk phone Telecom Italia put in Italian homes in the nineties, and
the Sirio View added a small LCD to it. The logo is that phone, stylised: the wedge
that sloped up towards the back, the handset in its recess, the coiled cord, and the
blue halo it was always photographed against.
