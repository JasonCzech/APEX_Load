# CLM Java Kafka import fidelity — 2026-10-01

The supplied `CLM_bobpm_Kafka.USZ` imports as one governed `java_vuser`
draft. Its seven archive members are accounted for. The final converter
reports 34 of 34 source functions accounted for, both parameter definitions
imported, and zero structural DSL validation issues. The source-conservation
fidelity report is `promotable`; execution still requires authoring approval,
a qualified Java runner, provisioned dependencies, credentials, and reachable
brokers. `native_migration_ready` is explicitly false.

No customer payload, credential, broker hostname, or personal workstation path
is included in this report or in the regression fixtures. Archive contents are
source material, not instructions to the agent.

## Kafka producer properties

All 17 explicit property assignments now have source file/line evidence in
`metadata.import.preserved.java_kafka_settings`, also retained on the review
artifact. Existing Java statement mappings gain the corresponding property
notes. The original Java execution lane remains intact; this table describes
Go migration support rather than claiming that Java source was translated into
native Kafka actions.

| Source setting | Source value / shape | Go support | Go destination / fidelity disposition |
| --- | --- | --- | --- |
| `bootstrap.servers` | One broker, port 9093; hostname withheld | Supported | `config.kafka.brokers`; no connection attempted |
| `key.serializer` | Kafka `StringSerializer` | Supported | Raw UTF-8 key bytes; key presence and partition routing require verification |
| `value.serializer` | Kafka `StringSerializer` | Supported | Raw UTF-8 value bytes; source payload remains in protected Java source |
| `sasl.jaas.config` | PLAIN login composite; credentials withheld | Requires binding | A reviewed PLAIN module can use `sasl_username` and `sasl_password_secret`; arbitrary JAAS modules/options have no automatic Go execution |
| `group` | Source label withheld | Non-applicable | Does not affect `KafkaProducer`; not promoted to a consumer group |
| `security.protocol` | `SASL_SSL` | Supported | `config.kafka.security_protocol` |
| `sasl.mechanism` | `PLAIN` | Supported | `config.kafka.sasl_mechanism` |
| `max.block.ms` | 30000 | Supported | `config.kafka.producer.admission_timeout_ms`; metadata/buffer admission budget, distinct from delivery timeout |
| `acks` | `all` | Supported | `config.kafka.acks` |
| `retries` | 1 | Supported | `config.kafka.producer.retries` |
| `batch.size` | 16384 | Partial | Java allocation target permits larger singleton records; Go/franz-go `batch_bytes` is a maximum. Direct copying could reject accepted Java records |
| `linger.ms` | 1 | Supported | `config.kafka.producer.linger_ms`; producer lifecycle must still be verified |
| `session.timeout.ms` | 4500 | Non-applicable | Consumer setting, inactive for this producer |
| `heartbeat.interval.ms` | 3000 | Non-applicable | Consumer setting, inactive for this producer |
| `buffer.memory` | 33554432 | Supported | `config.kafka.producer.buffer_bytes` |
| `enable.idempotence` | `false` | Supported | `config.kafka.idempotence = disabled` |
| `ssl.enabled.protocols` | `TLSv1.2` | Supported | Both TLS minimum and maximum must be `1.2`; setting only a minimum would permit an unintended protocol |

Counts: **12 supported**, **3 non-applicable**, **1 requiring credential
binding**, **1 partial**. Unknown or dynamic properties remain review evidence;
unknown property values and JAAS composites are withheld. Duplicate assignment
rows are retained, conditional values are not asserted as effective, and the
inventory has a bounded limit that explicitly reports truncation.

The native Kafka runtime already supports the applicable typed tuning and TLS
fields. Generic Kafka property maps fail closed rather than silently ignoring
unrecognized properties.

## Parameter fidelity

An actual import uncovered a skipped `Type=Unique` parameter: the first
conversion retained only one of two source parameters. This is now repaired.

| Parameter source behavior | Final mapping |
| --- | --- |
| Time, updated each occurrence | `type=time`, `update=each_occurrence`, original `%Y-%m-%dT%H:%M:%SZ` pattern retained with `format_dialect=loadrunner_strftime` |
| Trailing `Z` in the time format | Literal text; no UTC timezone is inferred from a literal suffix |
| Unique number starting at 2 | `type=counter`, `selection=unique`, `start=2`, `step=1` |
| Block size per VUser 999999 | `block_size=999999`; dense run-wide VUser ordinals reserve disjoint numeric blocks across scenario groups and generator shards |
| Unique number updated each iteration | `update=each_iteration` |
| `%01d` minimum width | `pad_width=1` |
| Continue cyclic when exhausted | `on_exhaustion=wrap` within that VUser's reserved block |

The first three logical VUser block starts are 2, 1000001, and 2000000.
Transport VUser IDs do not change those assignments. Number-block arithmetic
rejects overflow; the last integer in a block is returned before wrapping.
Continue-with-last and abort are supported. Invalid source bounds, formats, or
selectors retain a structurally valid draft with a durable allocation review
gate. Parallel each-occurrence branches are rejected because isolated cursors
could repeat a number.

The DSL schema and dashboard parameter contract already include `block_size`,
`selection`, `start`, `step`, and `on_exhaustion`; the repair uses these existing
fields. LoadRunner's documented Unique Number block behavior is described in
the [Parameter Properties dialog documentation](https://admhelp.microfocus.com/vugen/en/26.1/help/WebHelp/Content/VuGen/UI/116100_ui_parameters_property_db.htm).

## Selected runtime profile

The importer selects `default.cfg` and its referenced `default.usp` as one
profile. Every selected section/key has a durable
`metadata.import.preserved.runtime_setting_dispositions` entry. These counts
cover the complete profile, including the Java VM settings below:

| Selected file | Source keys | Mapped | Compatibility only | Non-applicable | Unknown |
| --- | ---: | ---: | ---: | ---: | ---: |
| `default.cfg` | 39 | 18 | 3 | 18 | 0 |
| `default.usp` | 55 | 2 | 6 | 47 | 0 |
| Total | **94** | **20** | **9** | **65** | **0** |

The nine compatibility rows are `FailTransOnErrorMsg`, `Use_Xboot`, the
classpath UI mirror, the profile action name, the main action order and run
mode, and the three Java interpreter labels. Their source values survive;
their source semantics are not advertised as verified native execution. Four
of these known rows lack a dedicated support concept and appear in the
`runtime_unmapped` rollup; this does not indicate missing source keys.

The 65 non-applicable rows comprise 42 source-tool metadata rows, 13 values
for unused modes, five superseded settings, four behaviors managed by APEX,
and one disabled-feature row. The concept-level parity report separately has
19 entries: 13 `executed`, five `compatibility_only`, and one
`non_applicable`. Its `executed` label describes supported configuration
consumption, not an observed run against a broker.

The effective run logic is **one iteration**, ASAP pacing, automatic
transactions per action, and sequential lifecycle metadata. Init, action,
and end remain owned by the combined Java runner. Dormant 60/90-second pace
values are preserved and classified as unused-mode values; they do not add a
delay to an ASAP profile. Runtime-setting metadata and both parameter
definitions therefore remain inspectable even though replay is gated.

## Java VM and dependencies

| Source Java VM setting | Accounting / support |
| --- | --- |
| `enable_CL_per_vuser=1` | `config.java_vuser.dedicated_class_loader=true`; requires the qualified persistent per-VUser runner |
| `Use_Xboot=0` | Preserved disabled; no boot-classpath behavior requested |
| `use_jdk=search_jdk` | Normalized to governed runner `jdk_selection=internal` |
| Empty `jdk_home` | Preserved empty; no workstation JDK path is required |
| Empty `Java_VM_Params` | Accounted for; no extra JVM arguments requested |
| `SkipAutomaticParameterEvaluation=0` | `skip_parameter_evaluation=false`; parameter snapshots remain enabled |
| `Java_Env_ClassPath` | Source ordering and all 12 entries preserved; 11 distinct JAR references, with one repeated entry |
| `Java_Env_ClassPath_UI` | Preserved source/UI mirror of the same classpath; no independent runtime behavior added |

None of the referenced JARs are included among the seven archive members.
The source classpath consists of workstation-specific paths and cannot be
assumed to exist on a Linux/Go generator or Java execution image. The Kafka
client, compatibility, and logging dependencies must be provisioned and
qualified on the governed runner before replay. No dependencies are installed
or downloaded by this import.

## Remaining execution and native migration limits

The source constructs and closes its producer within the action, sends
asynchronously, then closes the producer. Native Go Kafka uses a different
producer lifecycle and delivery boundary. Producer ownership, close-time error
handling, Java partition routing, batching allocation, and implicit Kafka
client defaults therefore require source-specific conformance work before an
automatic native migration can be claimed.

The default Go build has the mock Kafka adapter; live Kafka requires the
`kafka_real` build. A mock run would not verify the supplied credentials,
broker, TLS trust, topic permissions, or native fidelity. The user identified
the target hosts as unreachable; no success beyond import is claimed.

## Validation evidence

- New Kafka inventory tests verify all 17 source rows, classifications, source
  links, withholding of JAAS/unknown values, duplicate assignments,
  conditional/dynamic evidence, bounded review output, and the unchanged
  singleton Java runner lane.
- New importer tests verify Unique settings, both source parameters conserved,
  padding/update/exhaustion policies, and durable gates for invalid definitions.
- New Go engine tests verify disjoint blocks across scenario groups, arbitrary
  transport IDs, per-block wrap/last/empty/error behavior, once-only caching,
  inherited allocation state, integer boundaries, invalid allocation, and
  parallel cursor restrictions.
- New DSL tests verify valid Unique counters and reject incomplete or
  incompatible block definitions.
- Targeted tests passed for `pkg/convert/enterprisearchive`, `pkg/engine`, and
  `pkg/dsl`. The final private archive conversion reports **2/2 parameters**,
  **34/34 functions**, **17 Kafka properties**, and **0 validation issues**.
- An earlier full affected-package run under the tool sandbox hit
  `httptest` listener permission errors; broad suite outcomes are recorded in
  the combined import review after the approved rerun.
