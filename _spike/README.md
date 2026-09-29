# P0 spike

Throwaway. Answers four questions and gets deleted:

1. Does miniaudio resample for us when we ask a 48 kHz device for 8 kHz?
2. How much does the host audio clock drift against a 20 ms RTP cadence?
3. Does capturing from a virtual device need a signed `.app` bundle?
4. Can two BlackHole variants coexist as two independent devices?

## Setup

```sh
brew install --cask blackhole-2ch blackhole-16ch   # needs your password
brew install baresip
```

BlackHole 2ch stands in for `sirius-out`, the device a meeting client plays into.
BlackHole 16ch stands in for `sirius-in`, the device it captures from.

## Run

```sh
go run . -devices                       # list what CoreAudio reports
go run . -call sip:bridge@127.0.0.1:5080 -capture "BlackHole 2ch" -playback "BlackHole 16ch"
```

Start the softphone first so something answers:

```sh
baresip -f ./baresip-config
```

## What to look at

The spike prints a line every ten seconds with samples captured from the host,
samples handed to RTP, samples received from RTP, samples played back, and the
overruns and underruns on both rings. Drift shows up as a steadily growing gap
between the two counters in a direction, and as a rising overrun or underrun count.
A drift that stays near zero for ten minutes means a fixed jitter buffer is enough.
