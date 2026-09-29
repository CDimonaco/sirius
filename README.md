# Sirius

Answer your meetings on a desk phone. Sirius takes the audio of whatever is running
on your computer, Google Meet in a browser, Zoom, a Slack huddle, and bridges it to
a VoIP handset or to an analog phone behind an ATA.

It installs nothing into Slack, Meet or Zoom. No API token, no bot, no admin
approval. Sirius runs as a small SIP registrar on your own machine, and the meeting
client only ever sees an ordinary system microphone and speaker.

Status: design phase. Nothing works yet.

## Documentation

[docs/architecture/overview.md](docs/architecture/overview.md) has the design, the
session flow and the risks that are still open. Behaviour is specified under
[`openspec/`](openspec/).

## Development

Requires [asdf](https://asdf-vm.com). The Go version is pinned in `.tool-versions`.

```sh
asdf install
make build
make test
make lint
```

macOS is the first target, Linux the second.
