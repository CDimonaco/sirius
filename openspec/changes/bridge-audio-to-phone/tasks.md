## 1. Host audio behind an interface

- [ ] 1.1 Define the device interface in `internal/audio`: open a capture or playback device by name, read or write int16 mono at 8000 Hz, close, and report a device as lost.
- [ ] 1.2 Write the in-memory fake implementation that every later test uses, able to deliver a known sample sequence, stall on demand, and report itself lost.
- [ ] 1.3 Implement the malgo backend in `_darwin.go` and `_linux.go` files, with device lookup by name substring and the callback wired to the ring buffer.
- [ ] 1.4 Test that opening a missing device fails with the requested name in the error.

## 2. The ring buffer and its counters

- [ ] 2.1 Implement the bounded ring in `internal/media` with counted overruns and underruns, no allocation and no logging on the write path.
- [ ] 2.2 Test that a full ring counts the overrun and drops rather than blocking, and that an empty ring pads with silence and counts the underrun.
- [ ] 2.3 Test that a producer and a consumer at matched rates leave both counters at zero across a simulated ten minutes.

## 3. Codec and RTP boundary

- [ ] 3.1 Wrap diago's PCM decoder and encoder in `internal/media` so the rest of the code sees int16 samples and never a payload type.
- [ ] 3.2 Test encode and decode against fixed PCMU byte vectors in both directions.
- [ ] 3.3 Drop and count packets carrying a payload type that was not negotiated, and test that they never reach the decoder.

## 4. Jitter behaviour

- [ ] 4.1 Decide from a test, not from reading, whether diago's jitter buffer option absorbs a 200 millisecond stall and a silent sender. Record the answer in the change.
- [ ] 4.2 Implement whatever the previous task shows is missing, keeping steady-state occupancy near 60 milliseconds.
- [ ] 4.3 Test that a 200 millisecond gap is absorbed with continuous output and the call intact.
- [ ] 4.4 Test that a 5 second gap keeps the call up and records a stall.
- [ ] 4.5 Test that a sender whose sequence numbers advance while payload stops is counted as suppressed silence, not as a stall.

## 5. Registrar

- [ ] 5.1 Implement the registration table in `internal/sipsrv`: record a contact, grant an expiry clamped to 60 to 3600 seconds, expire it, and answer whether a phone is reachable. Take the clock as a dependency.
- [ ] 5.2 Wire `OnRegister` with digest authentication through `diago.NewDigestServer`.
- [ ] 5.3 Test the credential scenarios: valid registers, missing authorization is challenged with 401, wrong response gets 403, and neither records a contact.
- [ ] 5.4 Test expiry, clamping, refresh, unregistration with `Expires: 0`, and a missing `Contact` answered with 400.
- [ ] 5.5 Test that a second registration for the same account from a new address replaces the first.

## 6. Placing and ending a call

- [ ] 6.1 Implement call origination in `internal/sipsrv`, carrying the caller ID as the `From` display name, with a 30 second no-answer timeout.
- [ ] 6.2 Test the refusal when no phone is reachable, and that no device is opened in that case.
- [ ] 6.3 Test the no-answer timeout sends `CANCEL`, and that a `486` is reported as declined.
- [ ] 6.4 Test that PCMU is offered and that an answer without PCMU ends the call naming the mismatch.

## 7. The bridge

- [ ] 7.1 Implement the session in `internal/bridge`: ring, open both devices, couple them through `media`, and own the teardown order.
- [ ] 7.2 Handle a lost device by ending the call with the device named and closing the other one.
- [ ] 7.3 Test the end-to-end paths with fakes: a known sequence from capture arrives as PCMU in 20 millisecond packets, and a known PCMU sequence arrives at playback as PCM.
- [ ] 7.4 Test teardown from both directions, that both devices close, and that a second call after the first works.
- [ ] 7.5 Test that losing either device ends the call with that device named.

## 8. Wiring and closing the loop

- [ ] 8.1 Add configuration for the SIP account, bind address and port, and the two device names.
- [ ] 8.2 Make `cmd/sirius` run the registrar and place a call on a command, which is the smallest thing that exercises the capability without a tray.
- [ ] 8.3 Run the real path once against the phone and confirm audio in both directions, then record the ten minute counters.
- [ ] 8.4 Delete `_spike/` once nothing in it is still needed.
