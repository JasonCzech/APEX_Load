const pptxgen = require("pptxgenjs");
const React = require("react");
const ReactDOMServer = require("react-dom/server");
const sharp = require("sharp");
const FA = require("react-icons/fa");

// ---------------------------------------------------------------------------
// Palette — performance / telemetry / K8s-native, content-informed
// ---------------------------------------------------------------------------
const C = {
  ink:     "0E2233", // midnight navy (dark slides)
  inkDeep: "081726",
  panel:   "F4F8FB", // light content bg
  white:   "FFFFFF",
  teal:    "129DB2", // primary accent (telemetry)
  tealHi:  "27C4D8",
  green:   "2FAE78", // APEX leads / positive
  amber:   "E0913A", // partial / caution
  steel:   "5E7C92", // LRE / neutral
  red:     "C8553D", // trails badly
  text:    "152330",
  muted:   "5E7180",
  hair:    "DDE7ED",
  cardAlt: "EAF2F6",
  inkText: "C9D9E4", // light text on dark
  inkMute: "7E94A4",
};

const W = 13.333, H = 7.5, M = 0.62;
const RECT = "rect", RR = "roundRect", OVAL = "ellipse";
const mkShadow = () => ({ type: "outer", color: "0A1A28", blur: 7, offset: 3, angle: 90, opacity: 0.16 });

const pres = new pptxgen();
pres.defineLayout({ name: "WIDE", width: W, height: H });
pres.layout = "WIDE";
pres.author = "APEX Load";
pres.title = "APEX Load vs LoadRunner Enterprise";

// ---------------------------------------------------------------------------
// Icon rasterization (cached)
// ---------------------------------------------------------------------------
const iconCache = {};
async function icon(Comp, color, size = 256) {
  const key = Comp.name + color + size;
  if (iconCache[key]) return iconCache[key];
  const svg = ReactDOMServer.renderToStaticMarkup(
    React.createElement(Comp, { color, size: String(size) })
  );
  const png = await sharp(Buffer.from(svg)).png().toBuffer();
  const data = "image/png;base64," + png.toString("base64");
  iconCache[key] = data;
  return data;
}

// ---------------------------------------------------------------------------
// Reusable slide furniture
// ---------------------------------------------------------------------------
function footer(slide, n, dark = false) {
  const col = dark ? C.inkMute : C.muted;
  slide.addText("APEX Load  ·  Product comparison vs OpenText LoadRunner Enterprise", {
    x: M, y: H - 0.44, w: 9, h: 0.3, fontSize: 8.5, color: col, margin: 0, fontFace: "Arial",
  });
  slide.addText(String(n), {
    x: W - M - 0.6, y: H - 0.44, w: 0.6, h: 0.3, fontSize: 8.5, color: col,
    align: "right", margin: 0, fontFace: "Arial",
  });
}

function header(slide, iconData, kicker, title) {
  slide.addShape(OVAL, { x: M, y: 0.52, w: 0.64, h: 0.64, fill: { color: C.teal } });
  slide.addImage({ data: iconData, x: M + 0.16, y: 0.68, w: 0.32, h: 0.32 });
  slide.addText(kicker.toUpperCase(), {
    x: M + 0.84, y: 0.48, w: 11.5, h: 0.26, fontSize: 11, color: C.teal, bold: true,
    charSpacing: 2.5, margin: 0, fontFace: "Arial",
  });
  slide.addText(title, {
    x: M + 0.84, y: 0.71, w: 11.6, h: 0.55, fontSize: 26, bold: true, color: C.text,
    margin: 0, fontFace: "Georgia",
  });
  slide.addShape(RECT, { x: M, y: 1.42, w: W - 2 * M, h: 0.015, fill: { color: C.hair } });
}

function card(slide, x, y, w, h, accent, fill = C.white) {
  slide.addShape(RECT, { x, y, w, h, fill: { color: fill }, line: { color: C.hair, width: 1 }, shadow: mkShadow() });
  slide.addShape(RECT, { x, y, w: 0.09, h, fill: { color: accent } });
}

function pill(slide, x, y, text, color) {
  const w = 0.26 + text.length * 0.072;
  slide.addShape(RR, { x, y, w, h: 0.3, fill: { color }, rectRadius: 0.15 });
  slide.addText(text, { x, y, w, h: 0.3, fontSize: 9, bold: true, color: "FFFFFF", align: "center", valign: "middle", margin: 0, fontFace: "Arial" });
  return w;
}

function bullets(items, accent) {
  return items.map((t, i) => ({
    text: t,
    options: { bullet: { code: "2022", indent: 14 }, color: C.text, fontSize: 12.5,
      paraSpaceAfter: 7, breakLine: true, fontFace: "Arial", align: "left" },
  }));
}

// ===========================================================================
async function build() {
  // Preload icons
  const I = {
    clip:   await icon(FA.FaClipboardCheck, "#FFFFFF"),
    cubes:  await icon(FA.FaCubes, "#FFFFFF"),
    rocket: await icon(FA.FaRocket, "#FFFFFF"),
    bolt:   await icon(FA.FaBolt, "#FFFFFF"),
    net:    await icon(FA.FaProjectDiagram, "#FFFFFF"),
    imp:    await icon(FA.FaFileImport, "#FFFFFF"),
    chart:  await icon(FA.FaChartLine, "#FFFFFF"),
    shield: await icon(FA.FaShieldAlt, "#FFFFFF"),
    scale:  await icon(FA.FaBalanceScale, "#FFFFFF"),
    warn:   await icon(FA.FaExclamationTriangle, "#FFFFFF"),
    flag:   await icon(FA.FaFlagCheckered, "#FFFFFF"),
    up:     await icon(FA.FaArrowUp, "#2FAE78"),
    eq:     await icon(FA.FaEquals, "#5E7C92"),
    down:   await icon(FA.FaArrowDown, "#C8553D"),
    check:  await icon(FA.FaCheckCircle, "#2FAE78"),
    apexW:  await icon(FA.FaMountain, "#27C4D8"),
  };

  // =========================================================================
  // SLIDE 1 — TITLE (dark)
  // =========================================================================
  {
    const s = pres.addSlide();
    s.background = { color: C.ink };
    // top kicker rule
    s.addShape(RECT, { x: M, y: 1.5, w: 0.7, h: 0.06, fill: { color: C.tealHi } });
    s.addText("SOURCE-GROUNDED PRODUCT COMPARISON · JUNE 2026", {
      x: M, y: 1.66, w: 11, h: 0.3, fontSize: 12, color: C.tealHi, bold: true, charSpacing: 2, margin: 0, fontFace: "Arial",
    });
    s.addText([
      { text: "APEX Load", options: { color: C.white, bold: true, breakLine: true } },
      { text: "vs. LoadRunner Enterprise", options: { color: C.tealHi, bold: true } },
    ], { x: M, y: 2.05, w: 11.5, h: 1.9, fontSize: 52, fontFace: "Georgia", lineSpacingMultiple: 1.0, margin: 0 });

    s.addText(
      "Kubernetes-native performance testing, measured against the enterprise incumbent — deployability, scalability, protocol breadth, and the honest gaps.",
      { x: M, y: 4.0, w: 9.2, h: 0.9, fontSize: 15.5, color: C.inkText, margin: 0, fontFace: "Arial", lineSpacingMultiple: 1.2 }
    );

    // ramp-up bar motif (bottom right) — evokes a load ramp
    const heights = [0.5, 0.85, 1.2, 1.6, 2.05, 2.55];
    const shades = [C.steel, C.teal, C.teal, C.tealHi, C.green, C.green];
    let bx = 9.55;
    heights.forEach((hh, i) => {
      s.addShape(RR, { x: bx, y: 6.7 - hh, w: 0.46, h: hh, fill: { color: shades[i], transparency: 12 }, rectRadius: 0.05 });
      bx += 0.6;
    });

    // verdict strip
    s.addShape(RECT, { x: M, y: 5.55, w: 0.09, h: 0.95, fill: { color: C.green } });
    s.addText([
      { text: "APEX wins on architecture and day-to-day usability.  ", options: { color: C.white, bold: true } },
      { text: "LRE wins on migration intelligence, analysis depth, and battle-tested scale.", options: { color: C.inkText } },
    ], { x: M + 0.25, y: 5.5, w: 8.2, h: 1.0, fontSize: 13, fontFace: "Arial", valign: "middle", margin: 0, lineSpacingMultiple: 1.15 });

    s.addText("Audience: engineering · product · evaluators", {
      x: M, y: H - 0.5, w: 8, h: 0.3, fontSize: 9.5, color: C.inkMute, margin: 0, fontFace: "Arial",
    });
  }

  // =========================================================================
  // SLIDE 2 — EXECUTIVE SUMMARY
  // =========================================================================
  {
    const s = pres.addSlide();
    s.background = { color: C.panel };
    header(s, I.clip, "Executive summary", "The one-page verdict");

    // verdict band
    s.addShape(RECT, { x: M, y: 1.65, w: W - 2 * M, h: 0.92, fill: { color: C.ink }, shadow: mkShadow() });
    s.addShape(RECT, { x: M, y: 1.65, w: 0.1, h: 0.92, fill: { color: C.green } });
    s.addText([
      { text: "APEX Load is architecturally cleaner and more usable for day-to-day HTTP/API testing. ", options: { color: C.white, bold: true } },
      { text: "It trails LRE on correlation intelligence, browser fidelity, offline analysis depth, and proven scale.", options: { color: C.inkText } },
    ], { x: M + 0.3, y: 1.65, w: W - 2 * M - 0.6, h: 0.92, fontSize: 14.5, valign: "middle", margin: 0, fontFace: "Arial", lineSpacingMultiple: 1.12 });

    // stat callouts
    const stats = [
      ["10", "protocols compiled into every binary", C.teal],
      ["1", "web app: author → run → analyze → admin", C.green],
      ["~8 ns", "metrics hot-path / transaction (benchmarked)", C.teal],
      ["1–50", "generator pods, HPA/KEDA autoscaled", C.green],
    ];
    const sw = (W - 2 * M - 3 * 0.3) / 4;
    stats.forEach((st, i) => {
      const x = M + i * (sw + 0.3);
      card(s, x, 2.85, sw, 1.45, st[2]);
      s.addText(st[0], { x: x + 0.22, y: 2.98, w: sw - 0.3, h: 0.7, fontSize: 40, bold: true, color: st[2], margin: 0, fontFace: "Georgia" });
      s.addText(st[1], { x: x + 0.24, y: 3.72, w: sw - 0.42, h: 0.55, fontSize: 11, color: C.muted, margin: 0, fontFace: "Arial", lineSpacingMultiple: 1.05 });
    });

    // two-column when-to-choose
    const colW = (W - 2 * M - 0.3) / 2;
    card(s, M, 4.55, colW, 2.2, C.green);
    s.addText("WHERE APEX LOAD LEADS", { x: M + 0.3, y: 4.72, w: colW - 0.5, h: 0.3, fontSize: 12, bold: true, color: C.green, charSpacing: 1, margin: 0, fontFace: "Arial" });
    s.addText(bullets([
      "Goroutine engine + K8s-native HPA autoscaling",
      "Open JSON DSL + Parquet/JSONL open results",
      "Single pane of glass — no thick clients",
      "Live VUser scaling, pause/resume, duration tune",
      "Broad import paths: LR · JMeter · OpenAPI · HAR · k6",
    ]), { x: M + 0.32, y: 5.05, w: colW - 0.6, h: 1.6, valign: "top" });

    const x2 = M + colW + 0.3;
    card(s, x2, 4.55, colW, 2.2, C.steel);
    s.addText("WHERE LRE STILL LEADS", { x: x2 + 0.3, y: 4.72, w: colW - 0.5, h: 0.3, fontSize: 12, bold: true, color: C.steel, charSpacing: 1, margin: 0, fontFace: "Arial" });
    s.addText(bullets([
      "VuGen correlation intelligence & design studio",
      "TruClient DOM-aware browser record/replay",
      "Analysis: graph merge, page diagnostics, drill-down",
      "Decade-hardened, reference-scaled deployments",
      "Named load zones, Vault/mTLS, SAML, marketplace plugins",
    ]), { x: x2 + 0.32, y: 5.05, w: colW - 0.6, h: 1.6, valign: "top" });

    footer(s, 2);
  }

  // =========================================================================
  // SLIDE 3 — ARCHITECTURE AT A GLANCE
  // =========================================================================
  {
    const s = pres.addSlide();
    s.background = { color: C.panel };
    header(s, I.cubes, "Architecture", "Two different machines for the same job");

    const colW = (W - 2 * M - 0.4) / 2;
    const top = 1.7, ch = 4.75;

    // APEX column
    card(s, M, top, colW, ch, C.green);
    s.addShape(OVAL, { x: M + 0.3, y: top + 0.28, w: 0.5, h: 0.5, fill: { color: C.green } });
    s.addImage({ data: I.apexW, x: M + 0.4, y: top + 0.38, w: 0.3, h: 0.3 });
    s.addText("APEX Load", { x: M + 0.95, y: top + 0.26, w: colW - 1.2, h: 0.4, fontSize: 21, bold: true, color: C.text, margin: 0, fontFace: "Georgia" });
    s.addText("Cloud-native, single platform", { x: M + 0.95, y: top + 0.66, w: colW - 1.2, h: 0.3, fontSize: 11, color: C.muted, margin: 0, fontFace: "Arial" });
    s.addShape(RECT, { x: M + 0.3, y: top + 1.08, w: colW - 0.6, h: 0.012, fill: { color: C.hair } });
    s.addText(bullets([
      "Three Go binaries: controller, generator, apexctl CLI",
      "1 goroutine = 1 VUser (~2–4 KB stack)",
      "Generators as K8s StatefulSet; controller as Deployment (2 replicas)",
      "One React web app for authoring, run, analysis & admin",
      "SQLite by default; PostgreSQL (pgx) optional; Redis/Valkey for HA",
      "Telemetry: Prometheus scrape + t-digest percentiles + Dynatrace events",
    ]), { x: M + 0.34, y: top + 1.25, w: colW - 0.7, h: ch - 1.45, valign: "top" });

    // LRE column
    const x2 = M + colW + 0.4;
    card(s, x2, top, colW, ch, C.steel);
    s.addShape(OVAL, { x: x2 + 0.3, y: top + 0.28, w: 0.5, h: 0.5, fill: { color: C.steel } });
    s.addImage({ data: I.cubes, x: x2 + 0.4, y: top + 0.38, w: 0.3, h: 0.3 });
    s.addText("LoadRunner Enterprise", { x: x2 + 0.95, y: top + 0.26, w: colW - 1.2, h: 0.4, fontSize: 21, bold: true, color: C.text, margin: 0, fontFace: "Georgia" });
    s.addText("Controller + load-generator estate", { x: x2 + 0.95, y: top + 0.66, w: colW - 1.2, h: 0.3, fontSize: 11, color: C.muted, margin: 0, fontFace: "Arial" });
    s.addShape(RECT, { x: x2 + 0.3, y: top + 1.08, w: colW - 0.6, h: 0.012, fill: { color: C.hair } });
    s.addText(bullets([
      "Web portal + thick-client VuGen + Analysis desktop tool",
      "Process / thread VUsers on Load Generator hosts (often Windows)",
      "Controller orchestrates a fleet of provisioned LG VMs",
      "Proprietary C / JavaScript VUser scripts",
      "Enterprise RDBMS-backed; mature operational tooling",
      "Analysis engine: graph merge, page diagnostics, deep drill-down",
    ]), { x: x2 + 0.34, y: top + 1.25, w: colW - 0.7, h: ch - 1.45, valign: "top" });

    footer(s, 3);
  }

  // =========================================================================
  // SLIDE 4 — DEPLOYABILITY
  // =========================================================================
  {
    const s = pres.addSlide();
    s.background = { color: C.panel };
    header(s, I.rocket, "Deployability", "Ship it as containers, not an estate");

    // left: APEX deployment artifacts (icon rows)
    const lw = 7.2;
    card(s, M, 1.7, lw, 4.75, C.green);
    s.addText("APEX LOAD — DEPLOYS LIKE ANY K8S WORKLOAD", { x: M + 0.32, y: 1.88, w: lw - 0.6, h: 0.3, fontSize: 12.5, bold: true, color: C.green, charSpacing: 0.5, margin: 0, fontFace: "Arial" });
    const rows = [
      ["Helm charts + Kustomize overlays", "local, local-kind, HA, monitoring overlays in-repo"],
      ["StatefulSet generators + Deployment controller", "Linux containers; AKS-targeted, portable to any K8s"],
      ["HPA / KEDA autoscaling", "generator-keda-scaledobject.yaml — scale 1→50 on load"],
      ["NetworkPolicy + Service mesh-ready", "namespace isolation manifests shipped"],
      ["Prometheus ServiceMonitor + remote-write", "telemetry wired out of the box"],
      ["Redis/Valkey HA + Terraform IaC", "optional HA state; infra-as-code for AKS"],
    ];
    let ry = 2.32;
    rows.forEach((r) => {
      s.addShape(OVAL, { x: M + 0.34, y: ry + 0.02, w: 0.26, h: 0.26, fill: { color: C.cardAlt } });
      s.addImage({ data: I.check, x: M + 0.37, y: ry + 0.05, w: 0.2, h: 0.2 });
      s.addText([
        { text: r[0] + "   ", options: { bold: true, color: C.text, fontSize: 12.5 } },
        { text: r[1], options: { color: C.muted, fontSize: 10.5, italic: true } },
      ], { x: M + 0.74, y: ry - 0.04, w: lw - 1.1, h: 0.5, margin: 0, fontFace: "Arial", valign: "top", lineSpacingMultiple: 1.0 });
      ry += 0.67;
    });

    // right: LRE footprint + verdict callout
    const rx = M + lw + 0.35, rw = W - 2 * M - lw - 0.35;
    card(s, rx, 1.7, rw, 2.95, C.steel);
    s.addText("LRE FOOTPRINT", { x: rx + 0.3, y: 1.88, w: rw - 0.5, h: 0.3, fontSize: 12.5, bold: true, color: C.steel, charSpacing: 0.5, margin: 0, fontFace: "Arial" });
    s.addText(bullets([
      "Controller + provisioned LG hosts/VMs to stand up & license",
      "Windows-centric components for many protocol families",
      "Cloud LGs via vendor tooling; heavier per-host footprint",
      "Three installable products to version & patch",
    ]), { x: rx + 0.32, y: 2.25, w: rw - 0.62, h: 2.3, valign: "top" });

    s.addShape(RECT, { x: rx, y: 4.85, w: rw, h: 1.6, fill: { color: C.ink }, shadow: mkShadow() });
    s.addShape(RECT, { x: rx, y: 4.85, w: 0.09, h: 1.6, fill: { color: C.tealHi } });
    s.addText("VERDICT", { x: rx + 0.28, y: 5.0, w: rw - 0.5, h: 0.25, fontSize: 10.5, bold: true, color: C.tealHi, charSpacing: 1.5, margin: 0, fontFace: "Arial" });
    s.addText("One image set, declarative manifests, autoscaling for free. APEX leads on deployability for cloud-native shops; LRE's estate model is heavier but battle-tested.", {
      x: rx + 0.28, y: 5.28, w: rw - 0.55, h: 1.1, fontSize: 12, color: C.white, margin: 0, fontFace: "Arial", valign: "top", lineSpacingMultiple: 1.15 });

    footer(s, 4);
  }

  // =========================================================================
  // SLIDE 5 — SCALABILITY
  // =========================================================================
  {
    const s = pres.addSlide();
    s.background = { color: C.panel };
    header(s, I.bolt, "Scalability", "An efficient engine — with honest evidence limits");

    // left: engine efficiency
    const lw = 6.5;
    card(s, M, 1.7, lw, 2.55, C.green);
    s.addText("ENGINE EFFICIENCY (WIRED & TESTED)", { x: M + 0.32, y: 1.86, w: lw - 0.6, h: 0.3, fontSize: 12, bold: true, color: C.green, charSpacing: 0.5, margin: 0, fontFace: "Arial" });
    s.addText(bullets([
      "Goroutine-per-VUser — ~2–4 KB stack vs OS thread",
      "Zero-lock hot path: buffered metric channels (cap 10k)",
      "t-digest streaming percentiles; sync.Pool buffers cut GC",
      "Metrics hot path benchmarked at ~8 ns/op (reproducible)",
    ]), { x: M + 0.34, y: 2.2, w: lw - 0.7, h: 1.95, valign: "top" });

    // left-bottom: design targets
    card(s, M, 4.4, lw, 2.05, C.teal);
    s.addText("DESIGN TARGETS (UNBENCHMARKED)", { x: M + 0.32, y: 4.56, w: lw - 0.6, h: 0.3, fontSize: 12, bold: true, color: C.teal, charSpacing: 0.5, margin: 0, fontFace: "Arial" });
    s.addText(bullets([
      "10,000 HTTP VUsers / pod on 4 vCPU · 8 GB",
      "500 VUsers / second / pod ramp-up rate",
      "< 2 s metrics latency, generator → dashboard",
      "< 50 µs harness overhead per transaction",
    ]), { x: M + 0.34, y: 4.74, w: lw - 0.7, h: 1.55, valign: "top" });

    // right: honesty caveat + LRE
    const rx = M + lw + 0.35, rw = W - 2 * M - lw - 0.35;
    s.addShape(RECT, { x: rx, y: 1.7, w: rw, h: 2.7, fill: { color: "FBF1E2" }, line: { color: C.amber, width: 1 }, shadow: mkShadow() });
    s.addShape(RECT, { x: rx, y: 1.7, w: 0.09, h: 2.7, fill: { color: C.amber } });
    s.addShape(OVAL, { x: rx + 0.3, y: 1.92, w: 0.4, h: 0.4, fill: { color: C.amber } });
    s.addImage({ data: I.warn, x: rx + 0.39, y: 2.01, w: 0.22, h: 0.22 });
    s.addText("HONESTY CHECK", { x: rx + 0.84, y: 1.95, w: rw - 1.1, h: 0.35, fontSize: 13, bold: true, color: "9A6212", margin: 0, fontFace: "Arial", valign: "middle" });
    s.addText(bullets([
      "Only the metrics micro-benchmark is reproducible today.",
      "10k/pod, 50k total & ramp targets have NO published benchmark artifacts — they are aspirational design goals.",
      "Do not cite VUser density as achieved without hardware-specific evidence.",
    ]), { x: rx + 0.34, y: 2.5, w: rw - 0.7, h: 1.8, valign: "top" });

    card(s, rx, 4.55, rw, 1.9, C.steel);
    s.addText("LRE — PROVEN SCALE", { x: rx + 0.3, y: 4.72, w: rw - 0.5, h: 0.3, fontSize: 12, bold: true, color: C.steel, charSpacing: 0.5, margin: 0, fontFace: "Arial" });
    s.addText("Decade-hardened and reference-scaled across large enterprise deployments. This is LRE's clear edge: trusted numbers at very high VUser counts that APEX has not yet demonstrated.", {
      x: rx + 0.32, y: 5.06, w: rw - 0.62, h: 1.3, fontSize: 12, color: C.text, margin: 0, fontFace: "Arial", valign: "top", lineSpacingMultiple: 1.15 });

    footer(s, 5);
  }

  // =========================================================================
  // SLIDE 6 — PROTOCOL COVERAGE
  // =========================================================================
  {
    const s = pres.addSlide();
    s.background = { color: C.panel };
    header(s, I.net, "Protocols covered", "Ten protocol engines, compiled into every binary");

    const protos = [
      ["HTTP/1.1 & HTTP/2", "Core engine; the 10k-VUser/pod target path", "Supported", C.green],
      ["SOAP / WSDL", "WSDL import; WS-Security header management", "Supported", C.green],
      ["gRPC", "Unary + streaming; descriptor-set loading", "Supported", C.green],
      ["WebSocket", "Frames, ping/close, binary & compressed", "Supported", C.green],
      ["Java-over-HTTP", "Bounded base64 serialized-object codec (MVP)", "Supported", C.green],
      ["Database (SQL)", "database/sql query · exec · transactions", "Supported", C.green],
      ["Network suite", "DNS · TCP · UDP · FTP · SMTP · POP3 · IMAP · LDAP", "Supported", C.green],
      ["Kafka", "Mock broker default; real adapter via -tags kafka_real", "Partial", C.amber],
      ["JMS — Solace & IBM MQ", "Real broker execution needs an adapter", "Partial", C.amber],
      ["Browser", "Diagnostic canaries — NOT TruClient-equivalent", "Canary", C.steel],
    ];
    const cols = 2, gap = 0.3;
    const cw = (W - 2 * M - gap) / cols;
    const chh = 0.78, vgap = 0.12;
    const top = 1.62;
    protos.forEach((p, i) => {
      const r = Math.floor(i / cols), c = i % cols;
      const x = M + c * (cw + gap);
      const y = top + r * (chh + vgap);
      card(s, x, y, cw, chh, p[3]);
      s.addText(p[0], { x: x + 0.3, y: y + 0.1, w: cw - 1.7, h: 0.32, fontSize: 14, bold: true, color: C.text, margin: 0, fontFace: "Arial" });
      s.addText(p[1], { x: x + 0.3, y: y + 0.42, w: cw - 1.7, h: 0.3, fontSize: 10, color: C.muted, margin: 0, fontFace: "Arial" });
      pill(s, x + cw - 1.32, y + 0.24, p[2], p[3]);
    });

    // bottom note
    const noteY = top + 5 * (chh + vgap) + 0.04;
    s.addShape(RECT, { x: M, y: noteY, w: W - 2 * M, h: 0.66, fill: { color: C.ink } });
    s.addShape(RECT, { x: M, y: noteY, w: 0.09, h: 0.66, fill: { color: C.amber } });
    s.addText([
      { text: "Gap / non-goal:  ", options: { bold: true, color: C.amber } },
      { text: "SAP GUI, Citrix, Oracle Forms and other desktop-UI protocols — LRE and NeoLoad lead here. APEX routes these to review/external-runner paths, not native high-density load.", options: { color: C.inkText } },
    ], { x: M + 0.3, y: noteY, w: W - 2 * M - 0.6, h: 0.66, fontSize: 11.5, valign: "middle", margin: 0, fontFace: "Arial", lineSpacingMultiple: 1.1 });

    footer(s, 6);
  }

  // =========================================================================
  // SLIDE 7 — AUTHORING & MIGRATION
  // =========================================================================
  {
    const s = pres.addSlide();
    s.background = { color: C.panel };
    header(s, I.imp, "Authoring & migration", "Open scripts, and a real on-ramp off LoadRunner");

    const colW = (W - 2 * M - 0.4) / 2;
    // left: authoring
    card(s, M, 1.7, colW, 2.5, C.green);
    s.addText("AUTHORING", { x: M + 0.32, y: 1.86, w: colW - 0.6, h: 0.3, fontSize: 12.5, bold: true, color: C.green, charSpacing: 1, margin: 0, fontFace: "Arial" });
    s.addText(bullets([
      "Open JSON DSL — no proprietary C/JS VUser language",
      "Browser-based multi-protocol editor, single pane",
      "Template vars: {{env}}, {{data}}, {{var}}, {{fn.uuid()}}",
      "Governed deterministic assisted-authoring (LLM policy-gated)",
    ]), { x: M + 0.34, y: 2.2, w: colW - 0.7, h: 1.9, valign: "top" });

    // left-bottom: correlation
    card(s, M, 4.35, colW, 2.1, C.amber);
    s.addText("CORRELATION", { x: M + 0.32, y: 4.51, w: colW - 0.6, h: 0.3, fontSize: 12.5, bold: true, color: "9A6212", charSpacing: 1, margin: 0, fontFace: "Arial" });
    s.addText(bullets([
      "Ranked rules engine: ASP.NET state, CSRF/auth/session, cookies, redirects",
      "Replay-failure detection + guided/batch repair",
      "Honest gap: narrower than VuGen's design studio — LRE's defining moat",
    ]), { x: M + 0.34, y: 4.69, w: colW - 0.7, h: 1.65, valign: "top" });

    // right: migration / import
    const x2 = M + colW + 0.4;
    card(s, x2, 1.7, colW, 4.75, C.teal);
    s.addText("IMPORT & MIGRATION PATHS", { x: x2 + 0.32, y: 1.86, w: colW - 0.6, h: 0.3, fontSize: 12.5, bold: true, color: C.teal, charSpacing: 1, margin: 0, fontFace: "Arial" });
    s.addText("Protocol-aware LoadRunner/VuGen ZIP importer classifies the archive, then converts or routes to a review artifact — so migration is auditable, not blind.", {
      x: x2 + 0.34, y: 2.2, w: colW - 0.7, h: 0.85, fontSize: 12, color: C.text, margin: 0, fontFace: "Arial", valign: "top", lineSpacingMultiple: 1.15 });
    s.addText(bullets([
      "LoadRunner — Web HTTP/HTML, SOAP, DB, Network families",
      "JMeter .jmx test plans → DSL + scenario drafts",
      "OpenAPI · WSDL · HAR · raw HTTP / cURL samples",
      "DevWeb .js (static conversion) · k6 (HTTP subset) import/export",
      "AsyncAPI 2.x/3.x → Kafka DSL · result import (k6/Gatling/JMeter)",
      "GitHub (GHES) script pull/push & drift status",
    ]), { x: x2 + 0.34, y: 3.1, w: colW - 0.7, h: 3.2, valign: "top" });

    footer(s, 7);
  }

  // =========================================================================
  // SLIDE 8 — ANALYSIS, REPORTING & TELEMETRY
  // =========================================================================
  {
    const s = pres.addSlide();
    s.background = { color: C.panel };
    header(s, I.chart, "Analysis & telemetry", "Open formats and live signals — lighter offline depth");

    const cw = (W - 2 * M - 0.6) / 3;
    const blocks = [
      ["LIVE", C.green, [
        "WebSocket metric streams to the dashboard",
        "Prometheus /metrics scrape endpoint",
        "Dynatrace Events API v2 lifecycle hooks",
        "W3C traceparent propagation (HTTP/SOAP)",
      ]],
      ["ARCHIVE & REPORTING", C.teal, [
        "Open results: Parquet / JSONL (not proprietary .lrr)",
        "Archive report + single & multi-run comparison",
        "Release trend deltas with regression flags",
        "Executive/engineer PDF export from profiles",
      ]],
      ["HONEST GAPS", C.amber, [
        "Offline depth lighter than LRE Analysis (graph merge, page diagnostics)",
        "traceparent header only — no OTel span instrumentation/exporter",
        "No Word export; root-cause is deterministic, not APM-causal",
      ]],
    ];
    blocks.forEach((b, i) => {
      const x = M + i * (cw + 0.3);
      const accent = b[1];
      card(s, x, 1.7, cw, 4.75, accent);
      const hdrCol = b[0] === "HONEST GAPS" ? "9A6212" : accent;
      s.addText(b[0], { x: x + 0.3, y: 1.88, w: cw - 0.5, h: 0.3, fontSize: 12.5, bold: true, color: hdrCol, charSpacing: 0.8, margin: 0, fontFace: "Arial" });
      s.addShape(RECT, { x: x + 0.3, y: 2.22, w: cw - 0.6, h: 0.012, fill: { color: C.hair } });
      s.addText(bullets(b[2]), { x: x + 0.32, y: 2.4, w: cw - 0.64, h: 3.9, valign: "top" });
    });

    footer(s, 8);
  }

  // =========================================================================
  // SLIDE 9 — ENTERPRISE & GOVERNANCE
  // =========================================================================
  {
    const s = pres.addSlide();
    s.background = { color: C.panel };
    header(s, I.shield, "Enterprise & governance", "Largely at parity — a few deliberate trade-offs");

    const items = [
      ["AuthN / SSO", "OIDC + trusted-header", "Native SAML out of scope", "="],
      ["RBAC · service accounts · audit", "Implemented end-to-end", "Maps to domains/projects/roles", "="],
      ["Scheduling", "Cron + approvals + blackout windows", "+ capacity reservations & reserved pools", "="],
      ["Notifications", "Slack / Teams / Dynatrace webhooks", "No native email", "="],
      ["CI/CD", "GitHub Actions + signed action/release + JUnit/JSON", "No maintained cross-platform marketplace plugins", "▼"],
      ["Secrets / PKI", "Template refs + PII redaction", "No Vault, no mTLS", "▼"],
    ];
    const cw = (W - 2 * M - 0.3) / 2;
    const chh = 1.42, vgap = 0.16;
    items.forEach((it, i) => {
      const r = Math.floor(i / 2), c = i % 2;
      const x = M + c * (cw + 0.3);
      const y = 1.65 + r * (chh + vgap);
      const v = it[3];
      const accent = v === "=" ? C.green : C.amber;
      const vIcon = v === "=" ? I.eq : I.down;
      card(s, x, y, cw, chh, accent);
      s.addText(it[0], { x: x + 0.3, y: y + 0.16, w: cw - 1.3, h: 0.4, fontSize: 14.5, bold: true, color: C.text, margin: 0, fontFace: "Arial" });
      s.addText([
        { text: it[1], options: { color: C.text, fontSize: 11.5, breakLine: true } },
        { text: it[2], options: { color: C.muted, fontSize: 10.5, italic: true } },
      ], { x: x + 0.3, y: y + 0.6, w: cw - 1.3, h: 0.72, margin: 0, fontFace: "Arial", valign: "top", lineSpacingMultiple: 1.05 });
      // verdict chip
      s.addShape(OVAL, { x: x + cw - 0.78, y: y + 0.46, w: 0.5, h: 0.5, fill: { color: C.cardAlt } });
      s.addImage({ data: vIcon, x: x + cw - 0.66, y: y + 0.58, w: 0.26, h: 0.26 });
      s.addText(v === "=" ? "PARITY" : "TRAILS", { x: x + cw - 1.02, y: y + 0.98, w: 0.95, h: 0.22, fontSize: 8, bold: true, color: accent, align: "center", margin: 0, fontFace: "Arial" });
    });

    footer(s, 9);
  }

  // =========================================================================
  // SLIDE 10 — CAPABILITY SCORECARD
  // =========================================================================
  {
    const s = pres.addSlide();
    s.background = { color: C.panel };
    header(s, I.scale, "Capability scorecard", "Where each platform wins, at a glance");

    const head = (t) => ({ text: t, options: { fill: { color: C.ink }, color: C.white, bold: true, fontSize: 12, align: "left", valign: "middle", fontFace: "Arial" } });
    const verdictCell = (v) => {
      const map = { "▲▲": ["APEX leads big", C.green], "▲": ["APEX leads", C.green], "=": ["Parity", C.steel], "▼": ["APEX trails", C.amber], "▼▼": ["APEX trails badly", C.red] };
      const [label, col] = map[v];
      return { text: v + "  " + label, options: { color: "FFFFFF", fill: { color: col }, bold: true, fontSize: 10.5, align: "center", valign: "middle", fontFace: "Arial" } };
    };
    const cell = (t, b = false) => ({ text: t, options: { color: b ? C.text : C.muted, bold: b, fontSize: 10.8, align: "left", valign: "middle", fontFace: "Arial", margin: 3 } });

    const data = [
      [head("Capability area"), head("APEX Load"), head("LRE 26.x"), { text: "Verdict", options: { fill: { color: C.ink }, color: C.white, bold: true, fontSize: 12, align: "center", valign: "middle", fontFace: "Arial" } }],
      [cell("Engine efficiency", true), cell("Goroutine VUsers, K8s HPA"), cell("Process/thread VUsers on LG VMs"), verdictCell("▲")],
      [cell("Single pane of glass", true), cell("One web app, end to end"), cell("Portal + VuGen + Analysis"), verdictCell("▲")],
      [cell("Open results format", true), cell("Parquet / JSONL"), cell("Proprietary .lrr / .lra"), verdictCell("▲")],
      [cell("Live VUser controls", true), cell("Scale/pause/resume/duration"), cell("Possible, heavier"), verdictCell("▲")],
      [cell("Import breadth", true), cell("LR·JMeter·OpenAPI·HAR·k6…"), cell("LR-native; limited import"), verdictCell("▲")],
      [cell("Workload modeling", true), cell("Open+closed, pacing, goals"), cell("Implemented"), verdictCell("=")],
      [cell("Distributed execution", true), cell("Real gRPC multi-generator"), cell("Controller + LGs (mature)"), verdictCell("=")],
      [cell("Auth · RBAC · scheduling", true), cell("OIDC/header, cron+approvals"), cell("LDAP/SAML, timeslots"), verdictCell("=")],
      [cell("Correlation depth", true), cell("Ranked rule packs + repair"), cell("Full studio + learning"), verdictCell("▼")],
      [cell("Offline analysis depth", true), cell("Report + comparison + trend"), cell("Graph merge, drill-down"), verdictCell("▼")],
      [cell("Region / load zones", true), cell("Flat fleet targeting"), cell("Named hosts, cloud LGs"), verdictCell("▼")],
      [cell("Proven scale", true), cell("No benchmark artifacts yet"), cell("Decade-hardened"), verdictCell("▼")],
      [cell("Browser / SPA fidelity", true), cell("Diagnostic canaries only"), cell("TruClient DOM record/replay"), verdictCell("▼▼")],
    ];

    s.addTable(data, {
      x: M, y: 1.6, w: W - 2 * M, colW: [3.4, 3.5, 3.55, 1.65],
      rowH: 0.345, border: { type: "solid", pt: 0.5, color: C.hair },
      fill: { color: C.white }, valign: "middle",
      altRowFill: { color: "F0F5F9" },
    });

    footer(s, 10);
  }

  // =========================================================================
  // SLIDE 11 — WHERE APEX TRAILS (HONEST GAPS)
  // =========================================================================
  {
    const s = pres.addSlide();
    s.background = { color: C.panel };
    header(s, I.warn, "Honest gaps", "What to fix before claiming full LRE parity");

    const gaps = [
      ["Correlation intelligence", "Broaden response-family fixtures; UI-assisted medium-confidence review. Still trails VuGen's studio + learning.", "Effort: M"],
      ["Browser / SPA fidelity", "Browser VUsers are canaries — no DOM-aware capture. DOM record/replay is a separate large effort.", "Effort: L"],
      ["Proven scale", "Publish one reproducible density benchmark with hardware, generator class & protocol mix.", "Effort: S–M"],
      ["Offline analysis depth", "Multi-run trend polish, report-profile hardening, deeper transaction drill-down.", "Effort: M"],
      ["Region / load-zone geo", "Named self-hosted zones exist; add per-region response metrics & benchmark envelopes.", "Effort: M"],
      ["Enterprise hardening", "Real OTel spans, Vault/mTLS, SAML, maintained CI marketplace plugins.", "Effort: M each"],
    ];
    const cols = 3, gap = 0.3;
    const cw = (W - 2 * M - (cols - 1) * gap) / cols;
    const chh = 2.28, vgap = 0.2;
    gaps.forEach((g, i) => {
      const r = Math.floor(i / cols), c = i % cols;
      const x = M + c * (cw + gap);
      const y = 1.7 + r * (chh + vgap);
      card(s, x, y, cw, chh, C.amber);
      s.addText(g[0], { x: x + 0.28, y: y + 0.2, w: cw - 0.5, h: 0.6, fontSize: 14.5, bold: true, color: C.text, margin: 0, fontFace: "Georgia", valign: "top" });
      s.addText(g[1], { x: x + 0.28, y: y + 0.78, w: cw - 0.54, h: 1.1, fontSize: 11, color: C.muted, margin: 0, fontFace: "Arial", valign: "top", lineSpacingMultiple: 1.15 });
      s.addShape(RR, { x: x + 0.28, y: y + chh - 0.46, w: 1.2, h: 0.32, fill: { color: C.ink }, rectRadius: 0.06 });
      s.addText(g[2], { x: x + 0.28, y: y + chh - 0.46, w: 1.2, h: 0.32, fontSize: 9.5, bold: true, color: C.tealHi, align: "center", valign: "middle", margin: 0, fontFace: "Arial" });
    });

    footer(s, 11);
  }

  // =========================================================================
  // SLIDE 12 — VERDICT & RECOMMENDATION (dark)
  // =========================================================================
  {
    const s = pres.addSlide();
    s.background = { color: C.ink };
    s.addShape(OVAL, { x: M, y: 0.55, w: 0.64, h: 0.64, fill: { color: C.tealHi } });
    s.addImage({ data: I.flag, x: M + 0.16, y: 0.71, w: 0.32, h: 0.32 });
    s.addText("RECOMMENDATION", { x: M + 0.84, y: 0.52, w: 11, h: 0.26, fontSize: 11, color: C.tealHi, bold: true, charSpacing: 2.5, margin: 0, fontFace: "Arial" });
    s.addText("Choose the right tool for the workload", { x: M + 0.84, y: 0.74, w: 11.6, h: 0.55, fontSize: 26, bold: true, color: C.white, margin: 0, fontFace: "Georgia" });

    const colW = (W - 2 * M - 0.4) / 2;
    const top = 1.75, ch = 4.0;
    // APEX column
    s.addShape(RECT, { x: M, y: top, w: colW, h: ch, fill: { color: "13283A" }, line: { color: "1E3A50", width: 1 } });
    s.addShape(RECT, { x: M, y: top, w: colW, h: 0.62, fill: { color: C.green } });
    s.addText("CHOOSE APEX LOAD WHEN", { x: M + 0.3, y: top, w: colW - 0.5, h: 0.62, fontSize: 14, bold: true, color: "FFFFFF", valign: "middle", margin: 0, fontFace: "Arial" });
    s.addText([
      "You run on Kubernetes and want autoscaling for free",
      "HTTP/API/gRPC/WebSocket is the bulk of your load",
      "You value open scripts, open results, and CI-native flows",
      "You want one web app instead of three desktop tools",
      "Migrating off LoadRunner and want auditable imports",
    ].map((t) => ({ text: t, options: { bullet: { code: "2022", indent: 14 }, color: C.inkText, fontSize: 12.5, paraSpaceAfter: 9, breakLine: true, fontFace: "Arial" } })),
      { x: M + 0.34, y: top + 0.85, w: colW - 0.66, h: ch - 1.0, valign: "top" });

    // LRE column
    const x2 = M + colW + 0.4;
    s.addShape(RECT, { x: x2, y: top, w: colW, h: ch, fill: { color: "13283A" }, line: { color: "1E3A50", width: 1 } });
    s.addShape(RECT, { x: x2, y: top, w: colW, h: 0.62, fill: { color: C.steel } });
    s.addText("STAY ON LRE WHEN", { x: x2 + 0.3, y: top, w: colW - 0.5, h: 0.62, fontSize: 14, bold: true, color: "FFFFFF", valign: "middle", margin: 0, fontFace: "Arial" });
    s.addText([
      "You need SAP GUI, Citrix, or Oracle Forms load",
      "Deep VuGen correlation is mission-critical",
      "TruClient DOM-level browser fidelity is required",
      "You depend on Analysis's offline graph-merge depth",
      "Very high proven scale with vendor support is non-negotiable",
    ].map((t) => ({ text: t, options: { bullet: { code: "2022", indent: 14 }, color: C.inkText, fontSize: 12.5, paraSpaceAfter: 9, breakLine: true, fontFace: "Arial" } })),
      { x: x2 + 0.34, y: top + 0.85, w: colW - 0.66, h: ch - 1.0, valign: "top" });

    // closing line
    s.addShape(RECT, { x: M, y: 6.1, w: 0.09, h: 0.85, fill: { color: C.tealHi } });
    s.addText([
      { text: "Bottom line:  ", options: { bold: true, color: C.tealHi } },
      { text: "APEX Load is the stronger day-to-day HTTP/API platform and the cleaner deployment story. Close the correlation, browser, analysis, and proven-scale gaps and it becomes a credible full LRE replacement.", options: { color: C.white } },
    ], { x: M + 0.28, y: 6.05, w: W - 2 * M - 0.4, h: 0.95, fontSize: 13, valign: "middle", margin: 0, fontFace: "Arial", lineSpacingMultiple: 1.12 });
  }

  await pres.writeFile({ fileName: "APEX_Load_vs_LoadRunner_Enterprise.pptx" });
  console.log("WROTE APEX_Load_vs_LoadRunner_Enterprise.pptx");
}

build().catch((e) => { console.error(e); process.exit(1); });
