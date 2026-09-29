## Why

The P0 spike proved the audio path holds: ten minutes of two-way G.711 between a
browser on the host and a softphone on a separate device over wifi, with the last
three minutes and fifty seconds showing zero inserted and zero dropped samples. What
exists is throwaway code in `_spike/`, with no tests, no error handling and no way to
recover when a device disappears. This change turns the proven path into the first
real capability of the product.

It needs nothing from Slack, Meet or Zoom. Sirius reads the meeting audio from a
virtual input device and writes the phone audio to a virtual output device, so a
meeting client sees an ordinary system microphone and speaker and never learns that a
handset is involved.

## What Changes

- Sirius answers SIP `REGISTER` and remembers where to reach the phone, so the phone
  can be a plain endpoint with nothing configured beyond an address and credentials.
- Sirius originates a call to the registered phone with a caller ID string supplied by
  its caller, and tears the call down on hangup from either end.
- While the call is up, Sirius bridges PCM between a host capture device and a host
  playback device and the phone's RTP stream, encoding and decoding G.711 (PCMU).
- The receive path tolerates the two failure shapes the spike actually produced:
  discrete stalls of up to a few hundred milliseconds, and a sender that stops
  transmitting during silence.
- Losing a host audio device mid-call ends the call with a stated reason instead of
  writing into a buffer nobody drains, which is what the spike did.

## Capabilities

### New Capabilities

- `phone-registration`: accepting a phone's registration, tracking its expiry, and
  knowing whether a phone is currently reachable.
- `call-bridge`: placing and ending a call to the registered phone, and carrying audio
  in both directions between that call and a pair of host audio devices.

### Modified Capabilities

None. This is the first change in the project.

## Impact

New internal packages: `sipsrv` for the registrar and the user agent, `media` for
codec, RTP and jitter handling, `audio` for host device I/O, and `bridge` to own a
session's lifecycle. Host-specific audio code sits behind an interface so the Linux
port has somewhere to go.

New dependencies: `github.com/emiago/diago` and `github.com/emiago/sipgo` for SIP and
media, `github.com/gen2brain/malgo` for host audio. All three were exercised by the
spike.

Operational prerequisite: two virtual audio devices on the host. On macOS that means
`blackhole-2ch` and `blackhole-16ch`, and installing them is not enough, because
`coreaudiod` loads HAL plugins only at start. Capturing from a virtual device also
counts as microphone access, so a distributed build needs a signed `.app` bundle with
`NSMicrophoneUsageDescription`. That bundle is out of scope here and belongs with the
tray.

Out of scope for this change, and therefore still absent after it: any user interface,
resolving the meeting name, switching the host's default devices, DTMF control, and
everything already listed as later iterations.
