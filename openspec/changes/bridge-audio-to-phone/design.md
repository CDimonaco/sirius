## Context

A throwaway spike in `_spike/` already carried two-way G.711 between a browser on the
host and Zoiper on an iPhone over wifi, using diago for SIP and media and malgo for
host audio. It answered the questions that mattered:

- miniaudio resamples internally. Asking 8000 Hz from a device whose native rate is
  44100 Hz returned exactly 8000 samples per second, so there is no resampler to write.
- There is no clock drift to correct. Over ten minutes against a device with its own
  crystal, the last three minutes and fifty seconds inserted and dropped nothing, and
  the whole call accumulated 1520 samples of slack out of 4.8 million.
- The real failure shapes are discrete stalls of up to roughly 160 milliseconds, and a
  sender that stops transmitting while the far end is silent.
- A phone will not accept a direct call to an IP address. Zoiper on iOS requires an
  account, and a desk phone behaves the same way, so a registrar is not optional.
- Losing an audio device mid-call is silent and destructive. Restarting `coreaudiod`
  during a measurement left the spike writing into a buffer nobody drained.

This design turns that path into packages with tests, and it inherits the project's
hard constraint: nothing is installed into any meeting platform, and a meeting client
only ever sees a system microphone and speaker.

## Goals / Non-Goals

**Goals:**

- A phone registers to Sirius, is challenged for credentials, and becomes reachable.
- A call is placed to that phone with a caller ID string the caller supplies, and audio
  crosses in both directions until either end hangs up.
- The two measured failure shapes are handled rather than discovered again in
  production, and both are visible in counters.
- Host audio sits behind an interface, so the Linux port has somewhere to go.
- Everything above is testable without a phone, a meeting or an audio device.

**Non-Goals:**

- Any user interface, including the tray.
- Resolving the meeting name, and switching the host's default input and output.
- DTMF control, G.722 and Opus, calendar, more than one registered phone, recording,
  and a signed `.app` bundle.

## Decisions

### diago for SIP and media, rather than sipgo plus our own media stack

diago sits on sipgo and already provides G.711 encode and decode over an `io.Reader`
and `io.Writer`, RTP packetisation, a jitter buffer as a reader option, and DTMF in
both directions. The spike used all of it. Writing the media layer ourselves would mean
reimplementing packetisation and codec for no gain, against the rule that a new
dependency beats thirty lines only when it saves more than thirty lines. Here it saves
several hundred.

The cost is a dependency that owns the media session, so our audio code meets it at the
PCM boundary rather than at RTP. That boundary is `audio.PCMDecoderReader` and
`audio.PCMEncoderWriter`, which is exactly where a bridge wants to sit.

### The registrar is ours, on `sipgo.Server.OnRegister`

diago has no registrar; its `Register` is the client side. The spike proved a handler on
`sipgo.Server.OnRegister` coexists with diago's handlers, because they are different
methods, and that the server can be handed to diago with `WithServer`. Authentication
uses `diago.NewDigestServer().AuthorizeRequest`, which works on any request.

Authentication is in this change rather than deferred. A registrar open to the LAN that
accepts anything would let any device on the same wifi register and receive the user's
meeting audio.

### malgo for host audio, with the platform behind an interface

malgo binds miniaudio, which covers CoreAudio and ALSA and PulseAudio in one layer, so
the Linux port needs no second audio implementation. It also resamples internally,
which the spike measured. The alternative, our own CoreAudio cgo, would be more code
and would need writing twice.

The interface the rest of Sirius sees deals in devices that can be opened, closed, and
report themselves lost. malgo is one implementation of it, and tests use an in-memory
fake, which is what lets every scenario in the specs run with no hardware.

### Two separate devices rather than one duplex device

The two virtual devices are separate drivers with separate clocks, and CoreAudio cannot
open a duplex stream across two of them without an aggregate device. Two independent
streams also match what we measured, and give a stall on one direction its own counter.

### A counted ring buffer between the callback and the goroutine

The audio callback is real time: it cannot block, allocate or log. A bounded ring
buffer with counted overruns and underruns is what stands between it and the RTP
cadence. Counting rather than smoothing is deliberate, because the counters are the
only way the two measured failure shapes are visible at all.

### A fixed jitter buffer, not adaptive resampling

The earlier plan assumed adaptive resampling would be needed to chase two diverging
clocks. The measurement says the clocks do not diverge, so that work would solve a
problem that does not occur. What is needed instead is a buffer that absorbs a stall of
a couple of hundred milliseconds and keeps the playback device fed when the sender goes
quiet, holding around 60 milliseconds in steady state so the added latency stays small.

### Packages

`sipsrv` owns the user agent, the registrar and the registration table. `media` owns
the codec boundary, the ring buffer and the jitter behaviour. `audio` owns host devices
behind the interface above. `bridge` owns a session: it asks `sipsrv` to ring, opens the
devices through `audio`, couples them through `media`, and is the only place that knows
the order in which things are torn down.

## Risks / Trade-offs

- The host's address changes and the phone keeps sending to the old one → the phone
  re-registers on its own schedule, so the gap is bounded by the granted expiry. A DHCP
  reservation is the documented fix, and detecting the change is later work.
- A phone sends a payload type we never offered → unknown payload types are dropped and
  counted rather than decoded as PCMU, which would produce noise. Zoiper does this on
  payload type 95, and what it is remains unknown.
- Suppressed silence is mistaken for a stall → the two are counted separately, and the
  distinguishing signal is whether RTP sequence numbers advance while payload stops.
- A device disappears and the call hangs → specified to end the call with the lost
  device named, which is the one behaviour the spike got wrong.
- Capturing from a virtual device needs microphone permission, and an unsigned binary
  silently inherits the terminal's grant → development works and distribution does not.
  The signed bundle belongs with the tray, and until then this is a known trap.

## Migration Plan

Nothing to migrate. Setup on a fresh macOS host needs `blackhole-2ch` and
`blackhole-16ch` installed, and `coreaudiod` restarted afterwards, because HAL plugins
load only when that daemon starts. Without the restart the devices are invisible and
the failure looks like a broken driver.

## Open Questions

- What Zoiper sends on payload type 95, and whether a desk phone does anything similar.
- Whether diago's jitter buffer option covers both measured failure shapes, or whether
  the buffer has to be ours.
- Whether a silent sender needs generated comfort noise or whether silence is enough.
  The answer is audible, not measurable, so it needs a listener.
