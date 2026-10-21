# Rancher catalog wire contracts

The six declarations in types.go are derived from Rancher
[pkg/api/steve/catalog/types/rest.go](https://github.com/rancher/rancher/blob/0f5391d2f720ff0458d7103fbbf677c9c1daf48a/pkg/api/steve/catalog/types/rest.go)
at commit 0f5391d2f720ff0458d7103fbbf677c9c1daf48a.
They retain the original field order, raw JSON tags, and external field types.
The unused ChartUninstallAction is excluded. The original Go package name is retained to preserve raw encoding/json type errors.
The import path and package documentation are adapted for this provider; no Rancher runtime code is included.

Rancher distributes this source under Apache License 2.0; its license is
reproduced in LICENSE. The upstream root has no NOTICE file at this revision.

- Original rest.go SHA-256: 58bb336cca970b86d6eec9cfc68e815771b1a0fe6998e212c2f21b9a45200905
- Original LICENSE SHA-256: 809fa1ed21450f59827d1e9aec720bbc4b687434fa22283c6cb5dd82a47ab9c0

The JSON value and metadata oracle was first captured from the original types before extraction.
The final fixture was recaptured against the same immutable original source to include raw error strings and error types,
using legacy encoding/json. It covers every field, zero and populated values,
nil and empty containers, field metadata, external methods, and JSON errors.
The normalized package marker in the fixture records the intentional change
of private Go type ownership. Terraform and Rancher wire contracts stay stable.
