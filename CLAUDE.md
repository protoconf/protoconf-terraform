# CLAUDE.md

This file provides guidance to Claude Code (claude.ai/code) when working with code in this repository.

## What this project is

`protoconf-terraform` is a bridge between [Protoconf](https://www.protoconf.dev/) (Starlark-based, type-safe configuration) and Terraform. It does two distinct things:

1. **At authoring time (`generate`)**: introspects the Terraform providers in a working directory and emits `.proto` schemas that mirror each provider's resources/datasources/provider config. Those schemas become the typed surface that Protoconf `.mpconf`/`.pinc` files load to construct Terraform configs in Starlark.
2. **At runtime (`run`)**: subscribes to a Protoconf agent over gRPC, receives `Terraform` protobuf messages on each config update, materializes them as `main.tf.json` in a per-key working directory, then runs `terraform init` + `terraform apply -auto-approve`.

The CLI is one binary (`./cmd/protoconf-terraform`) wrapping three subcommands defined under `cmd/{init,generate,run}/`.

## Common commands

```bash
go build ./...                  # build everything
go build ./cmd/protoconf-terraform   # build just the CLI
go test -race ./...             # full test suite (CI runs this with -coverprofile)
go test ./pkg/importing -run TestGenerate -v   # single test (requires `terraform` in PATH — the test shells out to `terraform init` and `terraform providers schema -json`)
buf generate                    # regenerate Go code from proto/ (config.pb.go etc.) — uses buf.gen.yaml
goreleaser release --snapshot --clean   # local release dry-run
```

The CLI itself, once built:

```bash
protoconf-terraform init      [-output_path .]                 # extracts the embedded src/ template into the cwd
protoconf-terraform generate  [-import_path .] [-output src]   # writes terraform/**/*.proto from terraform providers schema
protoconf-terraform run       [-agent_address :4300] [-config_path test/main] [-terraform_root ...]
```

`run` also reads env vars prefixed with `PROTOCONF_` (e.g. `PROTOCONF_AGENT_ADDRESS`).

## Architecture — the two pipelines

### Pipeline 1: generate (`pkg/importing/`)

Entry: `generate.NewCommand` → `importing.NewGenerator(importPath, outputPath, ui)` → `PopulateProviders()` → `Save()`.

1. `parse.ParseTerraformSchema(path)` shells out: `terraform -chdir=path init`, then `terraform -chdir=path providers schema -json`, decoding the JSON into `parse.Providers` (which uses `cty.Type` for attribute types). It then runs `terraform version` and regex-parses provider versions back into the schema.
2. `Generator` builds one master file `terraform/v1/terraform.proto` containing the top-level `Terraform` message with nested `Resources` / `Datasources` / `Providers` messages, plus `Variable`, `Output`, `locals`, `module`, and `TerraformSettings` (with `Backend` oneof: local/remote/s3). All message construction goes through `jhump/protoreflect/desc/builder`.
3. For each provider, `NewProviderImporter` creates a per-(provider,kind,version,family) file under `terraform/<provider>/<resources|datasources|provider>/v<major>/<family>.proto`. The "family" is the second token of the resource name (e.g. `aws_s3_bucket` → family `s3`), which keeps file count manageable.
4. `schema_to_proto.go` does the cty→proto translation: `ctyTypeToProtoFieldType` maps string/number/bool/dynamic/object to proto types (dynamic and object both become `google.protobuf.Value`). Lists/sets/collections become repeated. Maps become proto `map<string, ...>`. Object types recursively generate nested messages via `handleObject`.
5. Every resource message gets meta fields appended (`for_each`, `count`, `depends_on`, `provider`, `lifecycle`) — these come from `meta.MetaFile()` and mirror Terraform's [meta-arguments](https://www.terraform.io/docs/configuration/resources.html#meta-arguments).
6. `Save()` adds the top-level Variable/Output/locals/module/TerraformSettings messages, registers the meta file, and writes all files via `protoconf/importers.Importer.SaveAll()`.

Field name fixup: any field starting with a non-letter is prefixed with `_` (proto field rule), but `json_name` keeps the original name so the resulting `main.tf.json` is still valid HCL JSON.

### Pipeline 2: run (`cmd/run/command.go`)

1. Parses `terraform/v1/terraform.proto` at startup with `protoparse.Parser{ImportPaths: []string{"src", ""}}` — the binary expects the generated proto tree to exist on disk relative to cwd. Builds an `anyResolver` that can dynamically materialize messages from `google.protobuf.Any` payloads.
2. Connects to the protoconf agent (gRPC, insecure) and subscribes to `c.config.ConfigPath`. The expected payload there is a `SubscriptionConfig{ keys: [...] }` listing per-target config keys.
3. Each key in the list is handed to a `keygroup.KeyGroup` (from `github.com/smintz/keygroup`) which manages one goroutine per key. The goroutine subscribes to that key, receives `Any`-wrapped `Terraform` messages, marshals them with `dynamic.Message.MarshalJSONIndent()`, writes to `<terraform_root>/<key>/main.tf.json`, and runs `terraform init` + `terraform apply -auto-approve` with `TF_LOG` set from `c.config.LogLevel`.
4. The whole loop is wrapped in `retry.Do` (avast/retry-go) — gRPC stream errors trigger reconnect; gRPC `Canceled` is treated as graceful shutdown. SIGINT/SIGTERM triggers `kg.CancelWait()`.

### init template (`embed.go` + `src/`)

`embed.go` does `//go:embed src` exposing `InitTemplate`. `cmd/init` walks that FS and writes everything into `OutputPath`. The embedded `src/terraform/v1/` ships the canonical hand-maintained `terraform.proto`, `meta.proto`, and `util.pinc` (the Starlark library that user `.mpconf` files import to build Terraform configs declaratively — see `example/src/example.mpconf`).

Note: `src/terraform/v1/terraform.proto` is hand-written and committed (it's the schema for top-level Terraform structure). The per-provider files under `terraform/<provider>/...` are *generated* and only exist after `protoconf-terraform generate` is run inside a workspace (see `example/src/terraform/{aws,null,random}/` for what that output looks like).

## Code generation

- `proto/protoconf_terraform/config/v1/config.proto` defines the CLI's own internal config messages (`TerraformInitCommand`, `TerraformPluginConfig`, `SubscriptionConfig`). Regenerated via `buf generate` (config also produces gRPC stubs but this proto has no services). `buf.yaml` excludes `src/` from buf builds because that tree contains generated/runtime-loaded protos that buf's lint rules shouldn't see.
- The `Version` constant in `pkg/build/build.go` is overridden via `-ldflags` by goreleaser at release time.

## Module versions worth knowing

`go.mod` pins `github.com/hashicorp/terraform v0.12.18` — that's an old release used only for its `plugin/discovery` package. Don't try to upgrade it casually; newer Terraform versions removed/relocated the plugin discovery API. Go module declares `go 1.18`; CI builds with Go 1.20.2; release with Go 1.21.

## Releases

`.goreleaser.yaml` cross-builds linux/darwin (no windows, no 386, no arm64-on-windows), publishes:
- a Homebrew formula to `protoconf/homebrew-tap` using `DEPLOY_GITHUB_TOKEN`
- Docker images to `protoconf/protoconf-terraform` (Docker Hub) and `ghcr.io/protoconf/protoconf-terraform` (linux/amd64 only, `build/Dockerfile` bases on `hashicorp/terraform:latest`).

Triggered by pushing a tag matching `*` (`.github/workflows/release.yml`).
