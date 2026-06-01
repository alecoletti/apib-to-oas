# Blueprint+

> Status: draft. Targets OpenAPI 3.0 / 3.1 / 3.2.

Blueprint+ is a **migration layer** on top of API Blueprint. It adds
the constructs API Blueprint never had so a converter can produce
faithful OpenAPI 3.x without losing information. It is *not* a way
to keep authoring APIB long-term — for greenfield work, write
OpenAPI directly.

Every valid API Blueprint document is a valid Blueprint+ document.
This spec only defines the deltas. For everything not mentioned
here, see [`apib.md`](apib.md).

---

## I. Sections Reference

### Document metadata

API Blueprint already supports arbitrary `KEY: value` lines before
the first `#`. Blueprint+ defines new reserved keys.

#### `VERSION`

```
VERSION: 1.2.0
```

Aliases: `API-VERSION`, `API_VERSION`. → `info.version`.

#### `SUMMARY`

```
SUMMARY: Reference articles API.
```

→ `info.summary`.

#### `LICENSE` / `LICENSE-ID` / `LICENSE-URL`

```
LICENSE:     Apache 2.0
LICENSE-ID:  Apache-2.0
LICENSE-URL: https://example.com/LICENSE
```

→ `info.license.{name, identifier, url}`.

#### `HOST` / `SERVER`

```
HOST:   https://api.prod.com    - Production
SERVER: https://api.sandbox.com - Sandbox
```

`HOST` and `SERVER` are aliases. May appear any number of times in
source order. The first ` - ` (space-dash-space) splits URL from
description.

→ `servers[]`.

#### `SECURITY`

```
SECURITY: BearerAuth, ApiKeyAuth
```

Comma-separated scheme names. Empty value (`SECURITY:`) clears the
default. → `/security`.

#### `WEBHOOK_GROUPS`

```
WEBHOOK_GROUPS: Notifications, Article Events
```

Comma-separated group titles whose resources route into `webhooks:`
instead of `paths:` (OAS 3.1+).

#### `ROOT.<key>`

Any metadata key may be prefixed with `ROOT.` to land on the
document root instead of `info`:

```
ROOT.Internal-Owners: platform-team
```

→ root `x-internal-owners`.

#### Unknown keys

Any other `Foo-Bar: value` line is preserved as `info.x-foo-bar`
(see [Extensions](#extensions)).

---

### `+ Meta` section

A new section type, placed immediately under a `# Group`,
`## Resource`, or `### Action` header — before any `+ Parameters`,
`+ Request`, or `+ Response`.

```
### Retrieve Article [GET]

+ Meta
    + OperationId: getArticle
    + Tags: Articles, Public
    + Deprecated: false
    + Docs: https://docs.example.com/getArticle
    + Security: BearerAuth
    + Idempotent: true
```


#### Reserved keys

| Key           | Maps to (action scope)             | Notes                                                                              |
|---------------|------------------------------------|------------------------------------------------------------------------------------|
| `OperationId` | `operationId`                      | Absent: derived from action title.                                                 |
| `Tags`        | `tags`                             | Replaces the inherited group tag. Prefix with `+` to append: `Tags: +Beta`.        |
| `Deprecated`  | `deprecated`                       | `true`/`false`/`yes`/`no`, case-insensitive.                                       |
| `Docs`        | `externalDocs.url` / `description` | Form: `Docs: <url> - <description>`.                                               |
| `Security`    | `security`                         | Comma-separated. Scopes via `Security: OAuth2 read,write`. Empty → `security: []`. |
| `Kind`        | `tags[].kind`                      | Group scope only, OAS 3.2+. Recognised: `nav`, `badge`, `audience`.                |

#### Scope

| Where the `+ Meta` block appears | Maps onto              |
|----------------------------------|------------------------|
| Group (`# Group <name>`)         | `tags[matching]`       |
| Resource (`## <Title> [...]`)    | `paths.<uri>`          |
| Action (`### <Title> [METHOD]`)  | `paths.<uri>.<method>` |

#### Unknown keys

Anything not in the reserved table is preserved as an `x-*`
extension on the same target object (see [Extensions](#extensions)).

---

### Parameters

API Blueprint declares parameters with MSON identifiers. Blueprint+
adds a description prefix to opt parameters out of the default
URI-template inference into header / cookie locations.

```
+ Parameters
    + traceId: abc123 (string, optional) - `[header:X-Trace-Id]` Trace id.
    + sessionId (string, optional) - `[cookie]` Auth cookie.
```

| Prefix                     | Meaning                                                                    |
|----------------------------|----------------------------------------------------------------------------|
| `` `[header]` ``           | `in: header`. The MSON identifier is the header name.                      |
| `` `[header:Real-Name]` `` | `in: header`, emitted as `Real-Name`. Lets dashed header names round-trip. |
| `` `[cookie]` ``           | `in: cookie`.                                                              |
| `` `[cookie:Real-Name]` `` | Cookie equivalent.                                                         |

The prefix must be at the very start of the description (whitespace
tolerated) and is stripped from the rendered description. When
present it overrides URI-template inference.

→ `parameters[]` on the matching `paths.<uri>.<method>`.

---

### Status descriptions

API Blueprint's `+ Response <code>` had no slot for a human-readable
status description. Blueprint+ adds an inline-title form:

```
+ Response 404 - Article not found.

    + Attributes (Error)
```

| OAS version | Inline title routes to         |
|-------------|--------------------------------|
| 3.0 / 3.1   | `responses.<code>.description` |
| 3.2+        | `responses.<code>.summary`     |

When omitted, the status falls back to the canonical IANA reason
phrase.

---

### MSON extensions

#### Format inference

```
+ id:     8c6e8f54-dc08-4e62-9a20-5e0c9e1c1234 (string)
+ joined: 2024-01-15T10:00:00Z (string)
+ email:  user@example.com (string)
+ home:   https://example.com (string)
```

Sample values matching unambiguous shapes (UUID, RFC 3339 date /
date-time, email, absolute URI) populate `format` on the property
schema.

#### `nullable`

```
+ middle_name (string, nullable)
```

→ on OAS 3.0: `nullable: true`. On 3.1 / 3.2: `type: ["string", "null"]`.

#### Type attributes: `fixed` and `fixed-type`

MSON supports two type attributes that constrain how much variation a
schema allows. Blueprint+ maps them to JSON Schema keywords:

| Type attribute | Applicable to | OAS / JSON Schema effect |
|---|---|---|
| `fixed-type` | `object` | `additionalProperties: false` — no extra keys, but properties keep their own `required`/`optional` |
| `fixed` | `object` | `additionalProperties: false` + every declared property becomes `required` |
| `fixed` | scalar (`string`, `number`, …) | `enum: [<example>]` — the only valid value is the example |

#### `fixed-type` — close the schema, keep properties optional

Use `fixed-type` when you want to reject unknown keys but still allow
some properties to be omitted:

```apib
# Data Structures

## CreateArticleRequest (object, fixed-type)
+ title:    Hello World (string, required)
+ subtitle  (string, optional)
+ tags      (array, optional)
```

```yaml
# Generated schema
type: object
additionalProperties: false   # no unknown keys
required:
  - title                     # only explicitly-required members
properties:
  title:   { type: string }
  subtitle: { type: string }
  tags:    { type: array }
```

#### `fixed` — lock the entire shape

Use `fixed` when the object must be provided exactly as declared — no
unknown keys **and** every declared property is required:

```apib
## Ping (object, fixed)
+ status: ok (string)
+ version: 1 (number)
```

```yaml
type: object
additionalProperties: false
required: [status, version]   # all properties promoted to required
properties:
  status:  { type: string }
  version: { type: number }
```

#### `fixed` on a scalar — constant value

For scalar types, `fixed` pins the only legal value to the example:

```apib
+ kind: article (string, fixed)
```

→ `enum: ["article"]`.

#### Enum descriptions

```
+ status: active (enum[string], required)
    + active   - User is active
    + disabled - User is disabled
```

Per-value descriptions populate `x-enum-descriptions[]` aligned
positionally with `enum[]`.

---

### `## SecuritySchemes (object)`

A reserved MSON named-type under `# Data Structures`. The name
matches case-insensitively. Each top-level member is one OAS
Security Scheme keyed by member name:

```
# Data Structures

## SecuritySchemes (object)
+ BearerAuth (object)
    + type: http
    + scheme: bearer
    + bearerFormat: JWT
    + description: `Short-lived JWT issued by /login.`

+ ApiKeyAuth (object)
    + type: apiKey
    + in: header
    + name: `X-API-Key`

+ OAuth2 (object)
    + type: oauth2
    + flows (object)
        + authorizationCode (object)
            + authorizationUrl: https://auth.example.com/authorize
            + tokenUrl:         https://auth.example.com/token
            + scopes (object)
                + read:  Read access.
                + write: Write access.

+ OIDC (object)
    + type: openIdConnect
    + openIdConnectUrl: https://auth.example.com/.well-known/openid-configuration
```

Field names mirror the OAS Security Scheme Object: `type`,
`description`, `name`, `in`, `scheme`, `bearerFormat`,
`openIdConnectUrl`, `flows`. Recognised `type` values: `http`,
`apiKey`, `oauth2`, `openIdConnect`, `mutualTLS` (3.1+).
Recognised OAuth2 flows: `implicit`, `password`,
`clientCredentials`, `authorizationCode`. Each flow's `scopes` is a
flat `key: description` map.

Values containing `-` or ` - ` must be wrapped in backticks —
Drafter splits unquoted MSON values on ` - ` into value /
description.

→ `components.securitySchemes.<MemberName>`. The reserved type is
*not* duplicated under `components.schemas`.

---

### Extensions

Any key on a metadata-bearing surface that isn't reserved by API
Blueprint or Blueprint+ is preserved as an `x-*` extension on the
matching OpenAPI object.

| Where the key appears        | Maps onto                               |
|------------------------------|-----------------------------------------|
| Document metadata            | `info.x-*` (or root via `ROOT.` prefix) |
| `+ Meta` under `# Group`     | `tags[matching].x-*`                    |
| `+ Meta` under `## Resource` | `paths.<uri>.x-*`                       |
| `+ Meta` under `### Action`  | `paths.<uri>.<method>.x-*`              |
| MSON member                  | property's `x-*`                        |

#### Key normalisation

Keys are split on `-`, `_`, and CamelCase boundaries; lowercased;
rejoined with `-`; prefixed with `x-`.

```
Retry-Policy: aggressive   →  x-retry-policy
RetryPolicy:  aggressive   →  x-retry-policy
retry_policy: aggressive   →  x-retry-policy
```

#### Value coercion

Values that parse cleanly as `true` / `false`, integers, or floats
are emitted as the corresponding JSON type. Everything else is a
string.

---

### Per-property constraints (`+ Meta` on MSON members / named types)

API Blueprint has no native syntax for JSON Schema validation
constraints (`pattern`, `minLength`, etc.). Blueprint+ rescues them
from an embedded `+ Meta` block inside the description of a MSON
member or named type.

#### On a named type

```apib
## ArticleSlug (string)
A URL-safe article identifier.

+ Meta
    + Pattern: `^[a-z0-9-]+$`
    + MinLength: 3
    + MaxLength: 64
```

#### On an inline member

```apib
+ slug: `my-article` (string) - A URL slug.

    + Meta
        + Pattern: `^[a-z0-9-]+$`
        + MaxLength: 64
```

> Stock Drafter folds the `+ Meta` block into the description text of
> the named type / member. The converter rescues it: the block is
> stripped from the rendered `description` and its keys are applied to
> the JSON Schema.

#### Recognised constraint keys

| Key                | JSON Schema field    | Type    | Notes                                        |
|--------------------|----------------------|---------|----------------------------------------------|
| `Pattern`          | `pattern`            | string  | Backtick-wrap recommended (avoids Drafter ` - ` split). |
| `MinLength`        | `minLength`          | integer | Strings and arrays of strings.               |
| `MaxLength`        | `maxLength`          | integer |                                              |
| `Minimum`          | `minimum`            | number  |                                              |
| `Maximum`          | `maximum`            | number  |                                              |
| `ExclusiveMinimum` | `exclusiveMinimum`   | number  |                                              |
| `ExclusiveMaximum` | `exclusiveMaximum`   | number  |                                              |
| `MultipleOf`       | `multipleOf`         | number  | Must be > 0.                                 |
| `MinItems`         | `minItems`           | integer | Arrays.                                      |
| `MaxItems`         | `maxItems`           | integer | Arrays.                                      |
| `UniqueItems`      | `uniqueItems`        | boolean | `true`/`false`/`yes`/`no`.                   |

Unknown keys in a `+ Meta` block under a member still fall through to
`x-*` extensions (see [Extensions](#extensions)).

---

## II. Appendix

### Webhook groups

The group title used in `WEBHOOK_GROUPS:` must match a `# Group`
title exactly. Resources inside a webhook group land under
`/webhooks/<group-name>` instead of `/paths/<uri>`. Webhook routing
requires OAS 3.1 or later; on 3.0 the metadata is ignored.

### Hierarchical tags (OAS 3.2+)

Nested `# Group` headers are flat tags on OAS 3.0 / 3.1. On OAS
3.2+ the inner group's tag carries `parent: <outer group title>`.

