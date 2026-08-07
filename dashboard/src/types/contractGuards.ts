// Shared helpers for the dashboard <-> Go transport-contract guards.
//
// The dashboard hand-writes ~4,900 lines of domain types that mirror Go DTOs.
// `types/generated/openapi.ts` is generated from the same Go handler
// annotations that produce docs/swagger.yaml, so it is the only machine-checked
// description of what the handlers actually accept and return. These helpers
// let a test compare a hand-written type against that generated contract and,
// just as importantly, refuse to pass when the generated side carries no
// information.
//
// This module is imported only by *.test.ts files under src/types. It is kept
// as a plain module (not a .test.ts) so several suites can share it; nothing in
// the application imports it, so it never reaches a production chunk.

/**
 * Keys of `T` that are declared without `?`.
 *
 * `Record<string, never>` is assignable to `{ a?: X }` (the index signature
 * satisfies the optional property) but not to `{ a: X }` (an index signature
 * never satisfies a required declared property), which is what separates the
 * two cases.
 */
export type RequiredKeysOf<T> = {
  [K in keyof T]-?: Record<string, never> extends Pick<T, K> ? never : K
}[keyof T]

/** Keys of `T` that are declared with `?`. */
export type OptionalKeysOf<T> = Exclude<keyof T, RequiredKeysOf<T>>

/**
 * True when `T` says nothing beyond "some JSON object".
 *
 * Responses, not request bodies, are the unschema'd surface. Measured against
 * `generated/openapi.ts` as it currently stands: 195 of the 394 JSON
 * 200-responses say nothing structural — 129 are `Record<string, unknown>`, 34
 * are `Record<string, string>`, 32 are bare `unknown` — because the Go swagger
 * annotation declares no schema. Request bodies are the opposite: 183 of 184
 * carry a real schema, and the single exception (`handleCreateDataPoolRow`) is
 * the partially-specified hybrid form described below rather than a bare map.
 * So request bodies, not responses, are where new guard coverage actually
 * compiles. Both figures are a snapshot of a generated file, not an invariant —
 * re-derive them before leaning on them.
 *
 * No structural assertion carries information against an untyped body, and the
 * failure is not uniform — which is how a contract guard rots into decoration.
 * For the index-signature shapes `keyof` is `string`, so `Exclude<keyof
 * Handwritten, keyof Generated>` and the scalar comparison pass vacuously while
 * `Exclude<keyof Generated, keyof Handwritten>` cannot reduce to `never` at all
 * and is a permanent compile error. For bare `unknown` the two swap: `keyof
 * unknown` is `never`, so `Exclude<keyof Generated, keyof Handwritten>` is the
 * one that passes vacuously and `Exclude<keyof Handwritten, keyof Generated>`
 * is the one that can never reduce to `never`. Either way the build breaks at
 * the wrong step without naming the cause. Guarded suites assert
 * `IsUntypedJsonObject<...>` is `false` for every operation they claim to
 * cover, so a regeneration that degrades a schema always produces one
 * diagnostic that does name the cause. It does not suppress the others: a
 * keyless body also breaks the alias projections a guard is built from
 * (`Body['members']` becomes "Property 'members' does not exist on type
 * '{}'"), so expect this assertion *plus* collateral, and read this one first.
 *
 * Detection is on `keyof`, not assignability: `Record<string, unknown>` is
 * assignable to any all-optional object type (TypeScript exempts types with an
 * index signature from weak-type detection), so an assignability probe would
 * report a perfectly well-specified all-optional request body as untyped. A
 * type that admits an arbitrary `string` key, by contrast, is exactly the
 * schema-less case — including the hybrid `{ known: X; [k: string]: unknown }`
 * bodies, which are only partially specified and equally unsafe to guard on.
 *
 * The second branch is the mirror case: a body with no keys at all. It tests
 * `keyof` rather than `[unknown] extends [T]` because callers do not
 * instantiate this on the raw generated type — the guarded aliases wrap it in
 * `NonNullable<...>` to strip the `| null` the generator adds unconditionally,
 * and since TypeScript 4.8 `NonNullable<T>` is `T & {}`, so a body that
 * degraded to bare `unknown` arrives here as `{}`. `[unknown] extends [{}]` is
 * false, so an `unknown`-shaped probe would report such a body as schema'd;
 * `keyof {}` and `keyof unknown` are both `never`, so the `keyof` test catches
 * the wrapped and unwrapped forms alike, along with any other keyless residue
 * of a degraded schema. Keyless is the right criterion rather than an
 * incidental one: with no keys, all three structural steps are vacuous or
 * uncompilable, which is precisely what this predicate exists to report.
 */
export type IsUntypedJsonObject<T> = string extends keyof T
  ? true
  : [keyof T] extends [never]
    ? true
    : false

/** A JSON scalar, or an array of them — the fields whose Go type is comparable. */
type ScalarLike = string | number | boolean

/** The keys of `T` whose value is a JSON scalar or an array of scalars. */
export type ScalarFieldNames<T> = {
  [K in keyof T]-?: NonNullable<T[K]> extends ScalarLike
    ? K
    : NonNullable<T[K]> extends readonly ScalarLike[]
      ? K
      : never
}[keyof T]

/** The keys `Handwritten` and `Generated` both declare. */
export type SharedKeys<Handwritten, Generated> = Extract<keyof Handwritten, keyof Generated>

/**
 * The keys a scalar comparison covers: declared by both sides, and modelled as
 * a scalar by *at least one* of them.
 *
 * The union — not the intersection — is what makes the comparison catch a
 * scalar turning into a struct. Intersecting would drop such a key from both
 * mapped types the moment either side stopped being scalar, which is exactly
 * the drift the comparison exists to catch.
 */
export type ComparableScalarKeys<Handwritten, Generated> = Extract<
  ScalarFieldNames<Handwritten> | ScalarFieldNames<Generated>,
  SharedKeys<Handwritten, Generated>
>

/**
 * What the generated side gets for a key it no longer models as a scalar.
 *
 * A string literal rather than `never` or a branded object because it is what
 * the reader sees in the `tsc` diagnostic: `Type 'string' is not assignable to
 * type '"CONTRACT DRIFT: ..."'` names the problem on the spot.
 */
type NonScalarOnGeneratedSide =
  'CONTRACT DRIFT: the generated contract no longer declares this field as a scalar'

/**
 * The dashboard's view of the compared fields, made required with
 * `null`/`undefined` stripped.
 *
 * A key-set comparison (`Exclude<keyof A, keyof B>`) catches a field appearing
 * or disappearing but says nothing about a field whose *type* moved — a Go
 * `string` becoming an `int` renames nothing, so the key sets stay identical and
 * both `tsc` and `vitest` stay green. Feeding both sides of a guard through
 * these helpers adds that missing half: `HandwrittenScalars<H, G>` must be
 * assignable to `GeneratedScalars<H, G>`, so a scalar whose Go type changes
 * fails to compile.
 *
 * Two deliberate exclusions keep the comparison honest rather than noisy:
 *
 *   * `null` and `undefined` are stripped. `pkg/api/openapi_contract_infer.go`
 *     marks every Go pointer field nullable unconditionally, without consulting
 *     the `omitempty` tag that actually decides whether the encoder emits `null`
 *     or omits the key, so `| null` on the generated side carries no
 *     information. Comparing it would produce false positives on fields the
 *     server can never send as `null`.
 *   * A field *both* sides model as an object or a map is skipped. The
 *     generator degrades opaque nested structs to `Record<string, unknown>` and
 *     marks nested sub-objects required, neither of which reflects the wire, so
 *     comparing two object types would report divergences the wire does not
 *     have. A field only *one* side models as a scalar is still compared — see
 *     `ComparableScalarKeys` — because that disagreement is drift, not
 *     generator imprecision.
 *
 * Keys only one side declares stay out of scope; those are already covered by
 * the key-set assertions.
 */
export type HandwrittenScalars<Handwritten, Generated> = {
  [K in Extract<
    keyof Handwritten,
    ComparableScalarKeys<Handwritten, Generated>
  >]-?: NonNullable<Handwritten[K]>
}

/**
 * The same key set as `HandwrittenScalars`, typed as the generated contract
 * declares it.
 *
 * A key the generated side no longer models as a scalar collapses to
 * `NonScalarOnGeneratedSide`, which nothing real is assignable to, so the
 * assertion fails instead of the key quietly dropping out of both sides.
 */
export type GeneratedScalars<Handwritten, Generated> = {
  [K in Extract<
    keyof Generated,
    ComparableScalarKeys<Handwritten, Generated>
  >]-?: K extends ScalarFieldNames<Generated>
    ? NonNullable<Generated[K]>
    : NonScalarOnGeneratedSide
}

/**
 * The runtime half of a contract guard: the keys the Go handler requires, and
 * (optionally) the full set it declares.
 *
 * `required`/`known` are plain string arrays so they can be checked against a
 * real serialized request body at run time. Each guarded suite pins them to the
 * generated type with `expectTypeOf`, so a Go-side change to the required set
 * fails `tsc` and a client-side change to the emitted body fails `vitest`.
 */
export interface RequestBodyContract {
  required: readonly string[]
  known?: readonly string[]
}

/**
 * Compare a decoded request body against a handler contract.
 *
 * Returns one human-readable violation per problem, so an assertion can be
 * `expect(contractViolations(body, contract)).toEqual([])` and still report
 * exactly what diverged.
 */
export function contractViolations(body: unknown, contract: RequestBodyContract): string[] {
  if (body === null || typeof body !== 'object' || Array.isArray(body)) {
    return [`body is ${Array.isArray(body) ? 'an array' : String(body)}, expected a JSON object`]
  }
  const record = body as Record<string, unknown>
  const violations: string[] = []
  for (const key of contract.required) {
    if (!Object.prototype.hasOwnProperty.call(record, key)) {
      violations.push(`missing required field "${key}"`)
      continue
    }
    if (record[key] === undefined) {
      violations.push(`required field "${key}" serializes to undefined`)
    }
  }
  if (contract.known) {
    for (const key of Object.keys(record)) {
      if (!contract.known.includes(key)) {
        violations.push(`field "${key}" is not declared by the handler contract`)
      }
    }
  }
  return violations
}

/** A single request the dashboard actually put on the wire. */
export interface CapturedRequest {
  url: string
  method: string
  rawBody: string | undefined
}

/**
 * Install a `fetch` stub that records every request and answers with `payload`.
 *
 * The point is to exercise the real `apiClient` (real URL construction, real
 * `JSON.stringify`, real headers) rather than mocking it away, so the captured
 * body is byte-for-byte what the controller would receive.
 */
export function captureFetch(payload: unknown = {}): {
  requests: CapturedRequest[]
  fetchStub: (input: unknown, init?: RequestInit) => Promise<Response>
} {
  const requests: CapturedRequest[] = []
  const fetchStub = (input: unknown, init?: RequestInit): Promise<Response> => {
    const body = init?.body
    requests.push({
      url: typeof input === 'string' ? input : String(input),
      method: (init?.method ?? 'GET').toUpperCase(),
      rawBody: typeof body === 'string' ? body : undefined,
    })
    return Promise.resolve(
      new Response(JSON.stringify(payload), {
        status: 200,
        headers: { 'Content-Type': 'application/json' },
      }),
    )
  }
  return { requests, fetchStub }
}

/** Decode the JSON body of a captured request, failing loudly if there is none. */
export function decodeCapturedBody(captured: CapturedRequest | undefined): unknown {
  if (!captured) throw new Error('no request was captured')
  if (captured.rawBody === undefined) {
    throw new Error(`${captured.method} ${captured.url} sent no request body`)
  }
  return JSON.parse(captured.rawBody)
}
