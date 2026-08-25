# Gemini OpenAI Normalizer for CLIProxyAPI

`gemini-openai-normalizer` is a configuration-free CLIProxyAPI response interceptor that makes Gemini/Antigravity responses conform more closely to the OpenAI Chat Completions and Responses schemas.

## What it fixes

For requested models whose names start with `gemini-`, the plugin:

- prefixes Chat Completions IDs with `chatcmpl-`;
- reports the requested model alias in `response.model`;
- includes reasoning tokens in `completion_tokens` when the upstream response proves it used the legacy accounting equation;
- includes reasoning tokens in Responses `output_tokens` under the equivalent condition;
- normalizes non-streaming JSON, raw JSON stream chunks, and SSE `data:` payloads;
- reassembles raw JSON and SSE events split across plugin ABI stream callbacks.

Every non-Gemini response passes through unchanged.

## Safety

Token accounting changes are conditional. The plugin only merges reasoning tokens when one of these exact legacy equations is true:

```text
total_tokens = prompt_tokens + completion_tokens + reasoning_tokens
total_tokens = input_tokens + output_tokens + reasoning_tokens
```

Already compliant usage objects are not modified, so reasoning tokens cannot be counted twice.

## Compatibility

- CLIProxyAPI plugin ABI v7
- Tested with CLIProxyAPI 7.2.141
- OpenAI Chat Completions
- OpenAI Responses
- Non-streaming and streaming responses
- Linux GLIBC 2.17 or newer

## Installation

The CLIProxyAPI plugin store listing is pending merge of [CLIProxyAPI-Plugins-Store PR #104](https://github.com/router-for-me/CLIProxyAPI-Plugins-Store/pull/104). After it is merged, install **Gemini OpenAI Normalizer** from the store and restart CLIProxyAPI when prompted. No plugin configuration is required.

Until then, install manually: download the archive for your platform from the latest GitHub Release, extract the single dynamic library into the configured CPA plugin directory, and restart CPA.

## Verification

```bash
go test ./...
go vet ./...
```

The tests cover requested-model routing, Chat ID/model/usage normalization, Responses usage normalization, already-compliant token accounting, raw JSON and SSE stream fragmentation, CRLF/multi-event SSE framing, bounded stream state, ABI envelopes, and non-Gemini passthrough.

## Release assets

GitHub Actions publishes store-compatible archives:

```text
gemini-openai-normalizer_<version>_<goos>_<goarch>.zip
checksums.txt
```

Each archive contains exactly one correctly named dynamic library at its root.
Linux archives are built in manylinux2014 and rejected during CI if they require symbols newer than GLIBC 2.17.

## License

MIT
