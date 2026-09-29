## ADDED Requirements

### Requirement: Sirius rings the registered phone with a caller ID it is given

Sirius SHALL place a call to the currently reachable contact, carrying a caller ID
string supplied by whoever asked for the call. Resolving that string from the running
meeting is not part of this capability.

#### Scenario: A call is placed to a reachable phone

- **GIVEN** a phone reported as reachable
- **WHEN** a call is requested with the caller ID `Standup`
- **THEN** an `INVITE` is sent to the recorded contact address
- **AND** its `From` header carries `Standup` as the display name

#### Scenario: No phone is reachable

- **GIVEN** no phone reported as reachable
- **WHEN** a call is requested
- **THEN** the request fails with an error naming the absent registration
- **AND** no host audio device is opened

#### Scenario: The phone does not answer

- **GIVEN** a call has been ringing for 30 seconds with no final response
- **WHEN** the timeout elapses
- **THEN** Sirius cancels the `INVITE`
- **AND** the caller is told the phone did not answer

#### Scenario: The phone declines

- **WHEN** the phone answers the `INVITE` with `486 Busy Here`
- **THEN** the caller is told the phone declined
- **AND** no host audio device is opened

### Requirement: Audio crosses the bridge in both directions

Once the phone answers, Sirius SHALL carry audio from a host capture device to the
phone and from the phone to a host playback device, encoded as G.711 PCMU at 8000 Hz
mono. It SHALL offer PCMU and SHALL refuse a call it cannot negotiate to PCMU.

#### Scenario: Negotiation settles on PCMU

- **WHEN** the phone answers offering PCMU among its codecs
- **THEN** the media session uses PCMU
- **AND** audio flows in both directions

#### Scenario: The phone offers no supported codec

- **WHEN** the phone answers with an answer containing no PCMU
- **THEN** Sirius ends the call with an error naming the codec mismatch

#### Scenario: Samples captured from the host reach the phone

- **GIVEN** an answered call
- **WHEN** a known sequence of PCM samples is presented by the capture device
- **THEN** the RTP stream sent to the phone carries the PCMU encoding of that sequence
  in packets of 20 milliseconds

#### Scenario: Samples received from the phone reach the host

- **GIVEN** an answered call
- **WHEN** RTP packets carrying a known PCMU sequence arrive
- **THEN** the playback device receives the decoded PCM of that sequence

#### Scenario: A ten minute call neither inserts nor drops audible audio

- **GIVEN** an answered call with continuous audio offered in both directions
- **WHEN** the call has run for ten minutes
- **THEN** inserted and dropped samples together stay below 0.1 per cent of the samples
  carried in each direction

### Requirement: The receive path absorbs a stalled sender

Sirius SHALL keep the call up and keep the playback device fed when the stream from the
phone stalls for tens to hundreds of milliseconds, and also when it stops entirely
because the far end is silent. Neither SHALL be treated as a failure.

#### Scenario: A short stall is absorbed

- **GIVEN** an answered call
- **WHEN** no RTP arrives for 200 milliseconds and then resumes
- **THEN** the call stays up
- **AND** the playback device is fed continuously across the gap

#### Scenario: A long stall is reported but does not end the call

- **WHEN** no RTP arrives for 5 seconds and then resumes
- **THEN** the call stays up
- **AND** the gap is recorded as a stall in the session's counters

#### Scenario: A silent sender does not look like a failure

- **GIVEN** a phone that suppresses silence and so stops sending audio packets
- **WHEN** it sends nothing for 3 seconds while the call is up
- **THEN** the playback device receives silence or comfort noise
- **AND** the counters distinguish suppressed silence from a stall

#### Scenario: Steady-state latency stays bounded

- **GIVEN** an answered call with audio arriving on schedule
- **WHEN** the buffer has settled
- **THEN** the buffer holds no more than 60 milliseconds of audio

#### Scenario: Jitter inside one packet time is not reported

- **GIVEN** an answered call
- **WHEN** a packet arrives 50 milliseconds after the one before it
- **THEN** no stall and no silence pause is counted

### Requirement: A call ends cleanly from either side

Sirius SHALL release the host devices and the media session whenever a call ends, by
whichever route, so that a later call starts from a clean state.

#### Scenario: The phone hangs up

- **GIVEN** an answered call
- **WHEN** the phone sends `BYE`
- **THEN** Sirius answers `200 OK`
- **AND** both host devices are closed
- **AND** the session reports itself as ended

#### Scenario: The host ends the call

- **GIVEN** an answered call
- **WHEN** the call is ended locally
- **THEN** Sirius sends `BYE` to the phone
- **AND** both host devices are closed

#### Scenario: A second call follows the first

- **GIVEN** a call that has ended
- **WHEN** a new call is requested
- **THEN** it opens the host devices again and carries audio in both directions

### Requirement: Losing a host audio device ends the call

Sirius SHALL end the call and state which device was lost when a host audio device
disappears mid-call, which happens when the audio daemon restarts or a driver is
removed. It MUST NOT keep writing into a buffer that nobody drains.

#### Scenario: The capture device disappears

- **GIVEN** an answered call
- **WHEN** the capture device stops delivering and reports itself as gone
- **THEN** Sirius ends the call with an error naming the lost device
- **AND** the remaining device is closed

#### Scenario: The playback device disappears

- **GIVEN** an answered call
- **WHEN** the playback device reports itself as gone
- **THEN** Sirius ends the call with an error naming the lost device
- **AND** the remaining device is closed
