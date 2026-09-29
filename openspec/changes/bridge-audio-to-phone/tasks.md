## 1. Host audio

- [x] 1.1 `internal/audio.Host` opens a capture or playback device by name substring at 8000 Hz mono int16, and reports a device as lost. No interface: there is one implementation, and the seam the tests need turned out to be `bridge.Phone`, not the device.
- [x] 1.2 Skipped the in-memory device fake. The ring is testable on its own and the pumps take plain functions, so no test needs a fake device.
- [x] 1.3 One malgo implementation for both platforms rather than `_darwin.go` and `_linux.go`. miniaudio already covers CoreAudio, ALSA and PulseAudio, so a split would be two copies of the same file. Split it when default-device switching arrives, which is genuinely per-platform.
- [x] 1.4 `Host.find` reports the requested name in the error.

## 2. The ring buffer and its counters

- [x] 2.1 `internal/audio.Ring`, in the same package as the devices it sits between rather than a package of its own.
- [x] 2.2 Tested, along with wrap-around.
- [x] 2.3 Tested over a simulated ten minutes.

## 3. Codec and RTP boundary

- [x] 3.1 `sipsrv.Call.ReadSamples` and `WriteSamples` hide the payload type behind int16 slices, wrapping diago's PCM decoder and encoder.
- [x] 3.2 Skipped the byte-vector tests. Nothing here implements G.711: it is diago's codec, tested upstream, and our wrapper is two unsafe reinterpretations with no branch to get wrong. Add them if we ever write a codec ourselves.
- [x] 3.3 Dropped this requirement. diago discards unsupported payload types inside `media/rtp_session.go` before any of our code runs, so counting them here would need a shadow counter that can never increment. The spec no longer claims we count them.

## 4. Jitter behaviour

- [x] 4.1 Did not reach for diago's jitter buffer at all. The ring already pads with silence and counts, which covers both measured shapes, so the buffer option would be a second mechanism doing the same job. Revisit if gaps turn out to be audible.
- [x] 4.2 No prebuffer. With matched rates the ring sits near empty, which satisfies the 60 millisecond ceiling by holding nothing. A prebuffer trades latency for stall tolerance and nothing has asked for that trade yet.
- [x] 4.3 Tested: a 200 millisecond gap keeps the call up, and the ring feeds silence across it.
- [x] 4.4 Tested: a long gap is counted as a stall and the call survives.
- [x] 4.5 Tested, using the RTP marker bit rather than sequence numbers. A phone marks the packet that starts a new talkspurt, which is the signal that separates a deliberate pause from a late packet.

## 5. Registrar

- [x] 5.1 `sipsrv.Registrar`, one contact slot rather than a table, because this iteration rings one phone.
- [x] 5.2 Wired, with the nonce cache given a one minute life instead of diago's five second default.
- [x] 5.3 Tested, including the full challenge and response handshake. Wrong credentials answer `401`, not `403`: that is what RFC 3261 prefers and what diago returns, and the spec now says so.
- [x] 5.4 Tested, including an expiry arriving as a `Contact` parameter instead of an `Expires` header.
- [x] 5.5 Tested.

## 6. Placing and ending a call

- [x] 6.1 `sipsrv.Server.Ring`, with the timeout carried by the context so diago sends the `CANCEL` itself.
- [x] 6.2 Covered by construction: `Ring` returns before any device is opened, because opening them is the bridge's job and the bridge never runs.
- [ ] 6.3 Not tested. Both paths need a peer that stalls or refuses, which means a fake SIP transport under diago. Cheaper to confirm against the phone, then decide whether the test earns its keep.
- [ ] 6.4 Not tested, same reason. PCMU-only is configured, and diago owns the negotiation failure.

## 7. The bridge

- [x] 7.1 `bridge.Run` opens both devices, runs the two pumps, and closes the devices on the way out whatever ended the call.
- [x] 7.2 A lost device wins the select and returns an error naming it. The deferred closes take care of the other one.
- [x] 7.3 Tested at the PCM boundary with a scripted phone: audio reaches the ring, and the ticker paces one packet per tick, padding with silence when nothing is buffered.
- [ ] 7.4 Teardown is covered by defers rather than by a test. A test would need the fake device from task 1.2, which nothing else needs.
- [ ] 7.5 Same: the select branch is two lines and needs a device that can be made to disappear.

## 8. Wiring and closing the loop

- [x] 8.1 Flags in `cmd/sirius`, not a config package. There is no file to load yet and no value that needs to outlive a command line.
- [x] 8.2 `cmd/sirius` waits for the registration, rings once, bridges, and prints the counters. `-devices` lists what the host reports.
- [ ] 8.3 Run the real path against the phone and record the counters.
- [ ] 8.4 Delete `_spike/` once 8.3 has passed.
