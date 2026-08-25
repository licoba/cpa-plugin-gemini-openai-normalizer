# Gemini OpenAI Normalizer for CLIProxyAPI

`gemini-openai-normalizer` is a configuration-free CLIProxyAPI response interceptor that makes Gemini/Antigravity responses conform more closely to the OpenAI Chat Completions and Responses schemas.

## What it fixes

For requested models whose names start with `gemini-`, the plugin:

- prefixes Chat Completions IDs with `chatcmpl-`;
- reports the requested model alias in `response.model`;
- includes reasoning tokens in `completion_tokens` when the upstream response proves it used the legacy accounting equation;
- includes reasoning tokens in Responses `output_tokens` under the equivalent condition;
- normalizes non-streaming JSON, raw JSON stream chunks, and SSE `data:` payloads.

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

## Installation

Install **Gemini OpenAI Normalizer** from the CLIProxyAPI plugin store and restart CLIProxyAPI when prompted. No plugin configuration is required.

Manual installation is also supported: download the archive for your platform from the latest GitHub Release, extract the single dynamic library into the configured CPA plugin directory, and restart CPA.

## Verification

```bash
go test ./...
go vet ./...
```

The tests cover Chat ID/model/usage normalization, Responses usage normalization, raw JSON stream chunks, SSE events, and non-Gemini passthrough.

## Release assets

GitHub Actions publishes store-compatible archives:

```text
gemini-openai-normalizer_<version>_<goos>_<goarch>.zip
checksums.txt
```

Each archive contains exactly one correctly named dynamic library at its root.

## License

MIT
