# AGENTS.md

Instructions for any coding agent working on this repository. They are ordered by
the cost of getting them wrong, so an earlier rule wins a conflict with a later one.

## The hard constraint

Sirius never integrates with a meeting platform. No Slack app, no Meet or Zoom API,
no bot, no OAuth grant, no admin approval. The platforms only ever see an ordinary
system microphone and speaker, and everything runs on the user's machine.

Never propose or implement anything that needs platform-side installation or
credentials, however convenient it looks. That property is what makes the project
worth building, and code that depends on a vendor API would have to be thrown away
for the next vendor. If a feature seems to require it, say so and stop.

## Never

- Push, open a pull request, or publish anything outside this machine without asking
  first. Committing locally needs no permission.
- List yourself as an author or co-author.
- Change code you haven't read.
- Treat a question as an instruction. "Should we use X" and "what would it take to
  add Y" are questions: answer them and stop. Ambiguous means question.
- Skip, delete or weaken a test to make the suite pass. Report the failure instead.

## What it is

A Go app that bridges meeting audio on the host to a physical desk phone over SIP,
acting as a minimal SIP registrar so it can originate the call itself. Read
`docs/architecture/overview.md` before touching anything. Behaviour is specified
under `openspec/`.

## Commands

```sh
asdf install     # toolchain comes from .tool-versions, not a system Go
make build       # -> bin/sirius
make test        # go test -race ./...
make lint        # golangci-lint
make fmt
```

Run `make test` and `make lint` before claiming work is done.

## Layout

- `cmd/sirius` holds the binary.
- `internal/` holds private packages. Create one when there is working code to put in
  it, never to reserve a name, and don't add documents that describe intent.
- `docs/architecture` holds the design.
- `openspec/` holds behaviour specs, authored with the `openspec` CLI.

macOS is the first target and Linux the second, so anything host-specific sits behind
an interface with `_darwin.go` and `_linux.go` implementations. That covers CoreAudio,
default-device switching and window titles. A platform call must never leak into
`bridge`, `media` or `sipsrv`.

## Code

- Do what was asked and nothing else. A bug fix carries no refactor, a feature
  carries no config options. If something else needs doing, say so in one sentence at
  the end and leave the code alone.
- Touch only the lines your change requires, with no incidental formatting edits.
- Match the file you're editing in naming, layout and error handling. Local
  convention wins over general best practice. Read a neighbouring file before adding
  a new one.
- Build the simplest thing that works today. Nothing for requirements that haven't
  arrived, in code, comments, tests or commit messages.
- Before writing new code, stop at the first of these that holds: it doesn't need to
  exist, it's already in this codebase, the stdlib does it, the platform does it, an
  existing dependency does it. A new dependency is the last resort. Thirty lines of
  G.711 beat pulling in a codec library.
- Comment the why: the tradeoff, the gotcha, the reason the obvious approach fails
  here. Never restate what the code does.
- Never name plans, tickets or workstreams in code, comments or commit messages.
- Wrap errors with `%w` and enough context to locate the failure. No `panic` outside
  `main` and genuine programmer errors.
- Every goroutine has an owner that can stop it and a defined exit path. Audio
  threads are real time, so the hot path allocates nothing, takes no lock and logs
  nothing.
- Delete dead code. No `_unused` renames, no note about where it was.
- Validate at boundaries: SIP messages, RTP packets, config files, anything that
  arrives off the network. Inside those boundaries trust internal calls, and don't
  handle errors that cannot happen.
- Be thorough about boundary validation, about errors that lose audio, and about
  clock handling. Clocks drift, devices lie about their sample rate, and the platform
  is never the spec.
- Commits use conventional style with a short subject and a body that says why.

## Testing

- Standard library `testing`, table driven, with no assertion framework.
- Unit test the deterministic core against fixed byte vectors: codec, RTP framing,
  jitter buffer, DTMF decoding, SDP negotiation. Correctness lives there and it needs
  no hardware.
- No test may require a real audio device, a real phone or a real meeting. Host audio
  and SIP transport sit behind interfaces with in-memory fakes. A test that genuinely
  needs hardware goes behind a `//go:build manual` tag and stays out of `make test`.
- Never use `time.Sleep` to synchronise. Inject clocks, synchronise on channels, and
  accept a `context.Context` on anything that blocks.
- Assert on measurable properties such as sample counts, drift, timestamps and buffer
  occupancy, never on whether something sounds right.

## Scope and honesty

- Calendar integration, G.722 and Opus, Linux support and speakerphone handling are
  out of the current iteration. Don't widen scope.
- Library capabilities and audio behaviour on this platform are assumptions until a
  spike proves them. Say which parts are unverified instead of asserting them.
- Look things up rather than recalling them. Use github.com for prior art and
  context7.com for library docs. If you couldn't reach them, say the answer came from
  memory.
