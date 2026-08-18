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
HOST:   https://api.dev.com     - Dev | dev-server
```

`HOST` and `SERVER` are aliases. May appear any number of times in
source order. The first ` - ` (space-dash-space) splits URL from
description. On OAS 3.2+ a trailing ` | name` suffix after the
description sets `server.name`:

```
HOST: https://api.prod.com - Production | prod
```

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
| `Summary`     | `tags[].summary`                   | Group scope only, OAS 3.2+. Short display name for the tag.                        |

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
    + legacySort (string, optional, deprecated) - Sort field (use `sort` instead).
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

The MSON `(deprecated)` type attribute on a parameter → `parameter.deprecated: true`:

```
+ sort (string, optional, deprecated) - Use `order_by` instead.
```

→ `parameters[]` on the matching `paths.<uri>.<method>`.

---

### Request headers

Standard API Blueprint `+ Headers` blocks are plain `Name: value`
pre-formatted code blocks — Drafter has no syntax for marking an
individual header as required. Blueprint+ rescues this with a trailing
parenthesised annotation on the value:

```apib
### Create Article [POST]

+ Request (application/json)
    + Headers

            Authorization: Bearer token (required)
            X-Idempotency-Key: 550e8400-e29b (required)
            X-Request-ID: abc-123
            Accept-Language: en
```

| Trailing annotation | OAS effect                                      |
|---------------------|-------------------------------------------------|
| `(required)`        | `parameters[].required: true`                   |
| `(optional)`        | `parameters[].required` omitted (default)       |
| `(deprecated)`      | `parameters[].deprecated: true`                 |
| *(absent)*          | `parameters[].required` omitted (default)       |

Annotations may be combined in any order:
`Authorization: Bearer token (required) (deprecated)`.

The annotations are **case-insensitive** and are **stripped from the OAS
`schema.example`** — the example value becomes the part before the first
annotation:

```yaml
# Input:  Authorization: Bearer token (required)
# Output:
parameters:
  - name: Authorization
    in: header
    required: true
    schema:
      type: string
      example: Bearer token
```

The same annotation convention applies to **response headers** (`+ Headers`
inside `+ Response`). Since OAS response headers are `Header` objects (not
`Parameter` objects), `(required)` maps to `header.required` and
`(deprecated)` maps to `header.deprecated`.

> **Why a value-field annotation and not MSON?**  
> Drafter parses `+ Headers` as a flat code block, not MSON. It folds
> the entire `Name: value (annotation)` line into the value field
> verbatim. Blueprint+ post-processes this field to rescue the
> annotation — no Drafter changes required. The convention follows the
> same parenthesised-annotation style MSON uses everywhere else
> (`(string, required)`, `(enum)`, etc.), making it readable to anyone
> already familiar with API Blueprint.

→ `parameters[]` with `in: header` on the matching
`paths.<uri>.<method>`.

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

#### `deprecated`

```
+ oldUsername (string, optional, deprecated) - Deprecated. Use `username` instead.
+ legacyScore (number, optional, deprecated) - Deprecated. Use `rating` instead.
```

→ `schema.deprecated: true` on the property schema. Applicable to any MSON member
(scalar, object, or array). The description after `-` is passed through as-is as
`schema.description` and should explain the reason and migration path.

This is the inline shorthand equivalent of placing `+ Meta` / `+ Deprecated: true`
in the member description — both produce the same output. The `(deprecated)` type
attribute is the preferred form when no other `+ Meta` keys are needed.

#### Description prefixes (`[deprecated]`, `[readOnly]`, `[writeOnly]`)

When a schema is defined using `### Properties`, Drafter parses members
structurally but drops `+ Meta` sub-blocks and unknown type attributes
(e.g. `deprecated` on named-type references like `(Cors, optional, nullable,
deprecated)`). Blueprint+ rescues these signals via a **description prefix**
convention — a bracket token at the very start of the member description:

```apib
+ id: `7066361a` (string, required) - [readOnly] Unique identifier.
+ cors (Cors, optional, nullable) - [deprecated] Use the `cors` plugin instead.
+ updatedAt (string, required) - [readOnly] [deprecated] Legacy timestamp.
+ password (string, required) - [writeOnly] User password.
```

| Prefix                           | JSON Schema field  | Notes                     |
|----------------------------------|--------------------|---------------------------|
| `[deprecated]`                   | `deprecated: true` | Works on any member type. |
| `[readOnly]` / `[read-only]`     | `readOnly: true`   | Works on any member type. |
| `[writeOnly]` / `[write-only]`   | `writeOnly: true`  | Works on any member type. |

- Prefixes are **case-insensitive** (`[DEPRECATED]`, `[ReadOnly]` are equivalent).
- Multiple prefixes may appear in any order: `[readOnly] [deprecated]`.
- Prefixes are **stripped from the rendered `description`** — only the prose
  after the last prefix appears in the OAS output.
- An unrecognised bracket token (e.g. `[header:X-Foo]`) is left in the
  description unchanged and stops prefix scanning.
- This approach works universally — with or without `### Properties` — and
  is the **only reliable way** to set `readOnly` / `writeOnly` / `deprecated`
  on named-type reference members inside `### Properties` blocks, where Drafter
  drops `+ Meta` sub-blocks entirely.

```yaml
# Generated schema for the examples above
properties:
  id:
    type: string
    readOnly: true
    description: Unique identifier.
  cors:
    allOf:
      - $ref: '#/components/schemas/Cors'
    nullable: true
    deprecated: true
    description: Use the `cors` plugin instead.
  updatedAt:
    type: string
    readOnly: true
    deprecated: true
    description: Legacy timestamp.
  password:
    type: string
    writeOnly: true
    description: User password.
```

#### Type attributes: `fixed` and `fixed-type`

MSON supports two type attributes that constrain how much variation a
schema allows. Blueprint+ maps them to JSON Schema keywords:

| Type attribute | Applicable to | OAS / JSON Schema effect |
|---|---|---|
| `fixed-type` | `object` | `additionalProperties: false` — no extra keys, but properties keep their own `required`/`optional` |
| `fixed` | `object` | `additionalProperties: false` + every declared property becomes `required` |
| `fixed` | scalar (`string`, `number`, …) | `enum: [<example>]` — the only valid value is the example |

> **Drafter normalisation:** Drafter emits `fixedType` (camelCase) in
> its Refract JSON for the `fixed-type` source keyword. The converter
> accepts both spellings. You always write `fixed-type` in your `.apib`
> source — the camelCase form is an internal Drafter detail.

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
| `ReadOnly`         | `readOnly`           | boolean | Marks a property as read-only (e.g. server-generated `id`). |
| `WriteOnly`        | `writeOnly`          | boolean | Marks a property as write-only (e.g. `password`). |
| `Deprecated`       | `deprecated`         | boolean | Marks the property schema as deprecated.     |
| `Const`            | `const` / `enum`     | any     | The only valid value. On OAS 3.1/3.2: `const: value`. On OAS 3.0: falls back to `enum: [value]` (JSON Schema draft-04 has no `const`). |
| `Discriminator`    | `discriminator.propertyName` | string | Named-type scope only. Sets the OAS `discriminator` object on a `oneOf` parent schema. The value is the property name used as a discriminator (e.g. `source`). Pair with a `One Of` block whose each variant declares that property as `(string, required, fixed)`. |

Unknown keys in a `+ Meta` block under a member still fall through to
`x-*` extensions (see [Extensions](#extensions)).

---

### `+ Schema Patch` (cross-field / conditional constraints)

MSON has no syntax for JSON Schema conditional applicators
(`if / then / else / not`). Blueprint+ provides a `+ Schema Patch`
escape hatch that merges a raw JSON Schema fragment onto the
generated schema for a named type *after* all MSON conversion is
done.

#### Syntax

Place a `+ Schema Patch` block inside a named type definition in
`# Data Structures`. The indented body must be a valid JSON object
whose keys are JSON Schema 2020-12 / OAS 3.1 conditional keywords.

```apib
## ArticleCreateRequest (object)
An article creation payload.

+ accessMode: public (string, required) - How the article is shared.
+ partnerAvailability (array[string]) - Partner IDs for exclusive articles.

+ Schema Patch
        {
          "if":   { "properties": { "accessMode": { "const": "exclusive" } },
                    "required":   [ "accessMode" ] },
          "then": { "required":   [ "partnerAvailability" ] },
          "else": { "properties": { "partnerAvailability": { "maxItems": 0 } } }
        }
```

The MSON members (`+ accessMode`, `+ partnerAvailability`) are
converted normally; the patch JSON is deep-merged on top of the
resulting schema. Stock Drafter folds the `+ Schema Patch` block
into the description text of the named type — the converter rescues
it, strips it from the rendered `description`, and applies it.

#### Recognised patch keys

| JSON Schema keyword | Notes                                           |
|---------------------|-------------------------------------------------|
| `if`                | Condition schema.                               |
| `then`              | Applied when `if` matches.                      |
| `else`              | Applied when `if` does not match.               |
| `not`               | Schema that must **not** match.                 |

Unknown keys in the patch object are silently ignored (forward
compatibility). A malformed JSON body is a no-op (the schema is
emitted without the patch).

> **First-write wins**: if a field is already set on the schema (e.g.
> a `not` from another source), the patch does not overwrite it.

#### OAS version note

`if / then / else` and `not` are valid in OAS 3.1+ (JSON Schema
2020-12 dialect). They are emitted as-is in OAS 3.0 output too, but
strict OAS 3.0 validators will flag them as unknown keywords.

---

#### `schema.examples` vs `schema.example` (OAS 3.1+)

The singular `example` keyword on Schema Objects is **deprecated** in
OAS 3.1 / 3.2 in favour of the JSON Schema 2020-12 `examples` array.
The converter automatically promotes `example: value` → `examples: [value]`
when `--oas-version` is 3.1 or 3.2, keeping 3.0 output unchanged.

Authors do not need to change their APIB source — the promotion is transparent.

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



