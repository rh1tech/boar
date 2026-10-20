/**
 * RH1 origami mark — extracted from rh1.tech/logo/origami.html.
 * Unfolds crease by crease from a folded packet into the flat logo.
 */

/*
 * The mark is a sheet of kami: orange on the front, white on the back.
 * Every crease in the logo runs at the same angle (the chamfer, the shadow
 * line, the top of the "1", the foot of the stem), so the animation starts
 * from a folded packet and opens it crease by crease until the flat logo
 * remains. Geometry is in the logo's own coordinates; faces are rotated in
 * 3D around their hinge and projected with a mild perspective.
 */
const SVG_NS = 'http://www.w3.org/2000/svg';
const CENTER = [127.49, 36.68];
const CAMERA = 130;          // perspective distance, in logo units
const LAYER = 0.02;          // z separation between stacked layers
const LIGHT = norm([-0.35, -0.55, 1]);

// Crease direction shared by every diagonal in the mark.
const DIAG = norm([146.45 - 111.85, 28.46 - 55.64, 0]);

const COLOR = {
  sheet: '#ff5722',
  sheetLowLeft: '#d94415',
  sheetLowRight: '#df4819',
  paper: '#ffffff',
  paperShade: '#e2e2e1',
  back: '#f4f1ec',
};

const hinges = {
  // Lower part of the sheet, below the long shadow crease.
  lower: { origin: [111.85, 55.64, LAYER * 3], dir: DIAG, side: [146.45, 55.64] },
  // Top-left corner, tucked behind along the chamfer.
  corner: { origin: [108.53, 25.19, 0], dir: norm([118.03 - 108.53, 17.72 - 25.19, 0]), side: [108.53, 17.72], behind: true },
  // The stem of the "1", turned over like a page from the left.
  stem: { origin: [131.18, 0, LAYER * 2], dir: [0, 1, 0], side: [136, 40] },
  // The beak of the "1", folded back out over the same edge.
  beak: { origin: [131.18, 0, LAYER * 3.5], dir: [0, 1, 0], side: [128, 31], parent: 'stem' },
};

// The stem's back is split along the mirrored shadow crease, so that before
// it turns over it matches the sheet it is lying on.
const faces = [
  { hinge: null, z: 0, front: COLOR.sheet, back: COLOR.back,
    pts: [[108.53, 25.19], [118.03, 17.72], [146.45, 17.72], [146.45, 28.46], [111.85, 55.64], [108.53, 55.64]] },
  { hinge: 'corner', z: 0, front: COLOR.sheet, back: COLOR.back,
    pts: [[108.53, 17.72], [118.03, 17.72], [108.53, 25.19]] },
  { hinge: 'lower', z: 0, front: COLOR.sheetLowLeft, back: COLOR.back,
    pts: [[111.85, 55.64], [131.15, 40.47], [131.15, 55.64]] },
  { hinge: 'lower', z: 0, front: COLOR.sheetLowRight, back: COLOR.back,
    pts: [[131.15, 40.47], [146.45, 28.46], [146.45, 55.64], [131.15, 55.64]] },
  // Seamless white underlay for the stem; only drawn face up.
  { hinge: 'stem', z: LAYER * 1.9, front: COLOR.paper, back: COLOR.paper, frontOnly: true, late: true,
    pts: [[131.18, 25.29], [140.81, 17.72], [140.81, 48.05], [131.18, 55.64]] },
  { hinge: 'stem', z: LAYER * 2, front: COLOR.paper, back: COLOR.sheet, late: true,
    pts: [[131.18, 25.29], [140.81, 17.72], [140.81, 32.89], [131.18, 32.89]] },
  { hinge: 'stem', z: LAYER * 2, front: COLOR.paperShade, back: COLOR.sheet, late: true,
    pts: [[131.18, 32.89], [140.81, 32.89], [131.18, 40.45]] },
  { hinge: 'stem', z: LAYER * 2, front: COLOR.paper, back: COLOR.sheet, late: true,
    pts: [[131.18, 40.45], [140.81, 32.89], [140.81, 48.05]] },
  { hinge: 'stem', z: LAYER * 2, front: COLOR.paper, back: COLOR.sheetLowLeft, late: true,
    pts: [[131.18, 40.45], [140.81, 48.05], [131.18, 55.64]] },
  { hinge: 'beak', z: LAYER * 4, front: COLOR.paper, back: COLOR.sheet, late: true,
    pts: [[121.52, 32.89], [131.18, 25.29], [131.18, 32.89]] },
];

// Gradient wash from the original mark, laid over the flat sheet.
const wash = { z: LAYER, pts: [[108.53, 25.19], [118.03, 17.72], [146.45, 17.72], [146.45, 55.64], [108.53, 55.64]] };

// Timeline in milliseconds. Angles are in degrees; 0 means lying flat in the logo.
const DURATION = 3400;
const timeline = {
  lower:  { from: 180, to: 0,   start: 250,  end: 1250, ease: easeInOutCubic },
  corner: { from: 0,   to: 180, start: 1050, end: 1750, ease: easeInOutCubic },
  stem:   { from: 180, to: 0,   start: 1550, end: 2450, ease: easeInOutCubic },
  beak:   { from: 180, to: 0,   start: 2300, end: 3050, ease: easeInOutCubic },
};
const ENTER = { start: 0, end: 400 };
const WASH = { start: 2700, end: 3400 };

// ---------------------------------------------------------------- math

function norm(v) {
  const l = Math.hypot(...v);
  return v.map((c) => c / l);
}
function sub(a, b) { return a.map((c, i) => c - b[i]); }
function add(a, b) { return a.map((c, i) => c + b[i]); }
function dot(a, b) { return a.reduce((s, c, i) => s + c * b[i], 0); }
function cross(a, b) {
  return [a[1] * b[2] - a[2] * b[1], a[2] * b[0] - a[0] * b[2], a[0] * b[1] - a[1] * b[0]];
}
function scale(v, s) { return v.map((c) => c * s); }

// Rodrigues rotation of v around unit axis k by angle a.
function rotate(v, k, a) {
  const c = Math.cos(a);
  const s = Math.sin(a);
  return add(add(scale(v, c), scale(cross(k, v), s)), scale(k, dot(k, v) * (1 - c)));
}

function clamp01(x) { return Math.min(1, Math.max(0, x)); }
function easeInOutCubic(t) { return t < 0.5 ? 4 * t * t * t : 1 - Math.pow(-2 * t + 2, 3) / 2; }
function progress(span, t) { return clamp01((t - span.start) / (span.end - span.start)); }

// Each hinge rotates its side of the crease towards the viewer (+z),
// or away from it when the fold goes behind the sheet.
for (const h of Object.values(hinges)) {
  const probe = sub([...h.side, 0], h.origin);
  const lifted = rotate(probe, h.dir, Math.PI / 2);
  const wantsUp = !h.behind;
  h.sign = (lifted[2] > 0) === wantsUp ? 1 : -1;
}

function hingeChain(name) {
  const chain = [];
  for (let n = name; n; n = hinges[n].parent) chain.push(n);
  return chain; // innermost first
}

function place(point, chain, angles) {
  let p = point;
  for (const name of chain) {
    const h = hinges[name];
    p = add(rotate(sub(p, h.origin), h.dir, h.sign * angles[name]), h.origin);
  }
  return p;
}

function project(p) {
  const k = CAMERA / (CAMERA - p[2]);
  return [CENTER[0] + (p[0] - CENTER[0]) * k, CENTER[1] + (p[1] - CENTER[1]) * k];
}

function signedArea(pts) {
  let a = 0;
  for (let i = 0; i < pts.length; i++) {
    const [x1, y1] = pts[i];
    const [x2, y2] = pts[(i + 1) % pts.length];
    a += x1 * y2 - x2 * y1;
  }
  return a / 2;
}

function shade(hex, f) {
  const n = parseInt(hex.slice(1), 16);
  const ch = [(n >> 16) & 255, (n >> 8) & 255, n & 255].map((c) => {
    const v = f <= 1 ? c * f : c + (255 - c) * (f - 1);
    return Math.round(Math.min(255, Math.max(0, v)));
  });
  return `rgb(${ch.join(',')})`;
}

// ---------------------------------------------------------------- render

const DEFAULT_SIZES = [16, 32, 64, 128, 256, 512];
const VIEW_BOX = '108.53 17.72 37.92 37.92';

for (const f of faces) {
  f.chain = f.hinge ? hingeChain(f.hinge) : [];
  f.restArea = Math.sign(signedArea(f.pts));
  f.casts = Boolean(f.hinge && !f.frontOnly);
}

const restLight = dot([0, 0, 1], LIGHT);

function pointList(pts) {
  return pts.map((p) => p.join(',')).join(' ');
}

// Pure geometry for time t, shared by every rendered size.
function computeFrame(t) {
  const angles = {};
  for (const [name, span] of Object.entries(timeline)) {
    const e = span.ease(progress(span, t));
    angles[name] = ((span.from + (span.to - span.from) * e) * Math.PI) / 180;
  }

  const drawn = faces.map((f, index) => {
    const world = f.pts.map((p) => place([p[0], p[1], f.z], f.chain, angles));
    const flat = world.map(project);
    const facingFront = Math.sign(signedArea(flat)) === f.restArea;
    const origin = place([0, 0, 0], f.chain, angles);
    const normal = sub(place([0, 0, 1], f.chain, angles), origin);
    const facing = facingFront ? normal : scale(normal, -1);
    const light = 0.7 + 0.3 * clamp01(dot(facing, LIGHT) / restLight) + 0.1 * Math.max(0, dot(facing, LIGHT) - restLight);
    const color = shade(facingFront ? f.front : f.back, light);
    // The stem rests where the lower flap is still folded away; it only
    // joins once the sheet underneath is down and it blends in.
    const hidden = (f.late && t < timeline.lower.end) || (f.frontOnly && !facingFront);

    // Lifted flaps cast a soft shadow down and to the right.
    const lift = Math.max(...world.map((p) => p[2]));
    const shadow = f.casts && {
      points: pointList(world.map((p) => [p[0] + p[2] * 0.35, p[1] + p[2] * 0.5])),
      opacity: (Math.min(1, lift / 6) * 0.22).toFixed(3),
    };

    const depth = world.reduce((sum, p) => sum + p[2], 0) / world.length;
    return { index, points: pointList(flat), color, hidden, shadow, depth };
  });
  drawn.push({ index: 'wash', depth: wash.z });
  drawn.sort((a, b) => a.depth - b.depth);

  const enter = easeInOutCubic(progress(ENTER, t));
  return {
    drawn,
    washOpacity: easeInOutCubic(progress(WASH, t)).toFixed(3),
    opacity: enter,
    transform: `scale(${0.9 + 0.1 * enter})`,
  };
}

function svgEl(name, attrs = {}, parent) {
  const el = document.createElementNS(SVG_NS, name);
  for (const [k, v] of Object.entries(attrs)) el.setAttribute(k, v);
  if (parent) parent.appendChild(el);
  return el;
}

// Builds one SVG at the given pixel size. Gradient and filter ids are
// unique per instance so marks never reference each other's defs.
function createMark(size, n) {
  const svg = svgEl('svg', { viewBox: VIEW_BOX, width: size, height: size, 'aria-hidden': 'true' });
  const defs = svgEl('defs', {}, svg);
  const gradient = svgEl('linearGradient', {
    id: `light-${n}`, gradientUnits: 'userSpaceOnUse', x1: 0, y1: 17.72, x2: 0, y2: 55.64,
  }, defs);
  const stops = [[0, '#fff', 0.12], [0.2, '#fff', 0.06], [0.4, '#fff', 0.01], [0.6, '#000', 0.04], [0.8, '#000', 0.13], [1, '#000', 0.28]];
  for (const [offset, color, opacity] of stops) {
    svgEl('stop', { offset, 'stop-color': color, 'stop-opacity': opacity }, gradient);
  }
  const filter = svgEl('filter', { id: `soft-${n}`, x: '-50%', y: '-50%', width: '200%', height: '200%' }, defs);
  svgEl('feGaussianBlur', { stdDeviation: 0.8 }, filter);

  const shadowLayer = svgEl('g', { filter: `url(#soft-${n})` }, svg);
  const faceLayer = svgEl('g', {}, svg);
  const polygon = (parent) => svgEl('polygon', { 'stroke-linejoin': 'round', 'stroke-width': 0.12 }, parent);

  const els = { wash: svgEl('polygon', { fill: `url(#light-${n})`, points: pointList(wash.pts) }, faceLayer) };
  const shadows = {};
  faces.forEach((f, i) => {
    els[i] = polygon(faceLayer);
    if (f.casts) shadows[i] = svgEl('polygon', { fill: '#000' }, shadowLayer);
  });

  return { svg, faceLayer, els, shadows };
}

function paint(mark, data) {
  for (const d of data.drawn) {
    const el = mark.els[d.index];
    mark.faceLayer.appendChild(el);
    if (d.index === 'wash') {
      el.setAttribute('opacity', data.washOpacity);
      continue;
    }
    const visibility = d.hidden ? 'hidden' : 'visible';
    el.style.visibility = visibility;
    el.setAttribute('points', d.points);
    el.setAttribute('fill', d.color);
    el.setAttribute('stroke', d.color);
    const shadow = mark.shadows[d.index];
    if (shadow) {
      shadow.style.visibility = visibility;
      shadow.setAttribute('points', d.shadow.points);
      shadow.setAttribute('opacity', d.shadow.opacity);
    }
  }
  mark.svg.style.opacity = data.opacity;
  mark.svg.style.transform = data.transform;
}



function createRh1Mark(size = 18, id = 0) {
  return createMark(size, id);
}

function paintRh1Mark(mark, t) {
  paint(mark, computeFrame(t));
}

/**
 * Mount an animated RH1 mark into `host`.
 * Loop: folded (nothing) → short pause → unfold → long pause → fold → short pause → …
 * `pauseMs` is the hold while the logo is open; `pauseFoldedMs` is the brief beat while folded.
 * Returns a dispose function.
 */
function mountRh1Mark(
  host,
  { size = 18, pauseMs = 3000, pauseFoldedMs = 200, id = 0, startOpen = false } = {},
) {
  host.replaceChildren();
  const mark = createMark(size, id);
  mark.svg.style.display = "block";
  mark.svg.style.overflow = "visible";
  host.appendChild(mark.svg);

  let disposed = false;
  let running = false;
  let startedAt = 0;
  let timer = 0;
  let raf = 0;
  /** `1` = unfold forward, `-1` = fold back. */
  let direction = startOpen ? -1 : 1;

  const reduced =
    typeof matchMedia === "function" &&
    matchMedia("(prefers-reduced-motion: reduce)").matches;

  const frame = (t) => paint(mark, computeFrame(t));

  const tick = (now) => {
    if (disposed) return;
    const elapsed = now - startedAt;
    const t = direction === 1 ? Math.min(elapsed, DURATION) : Math.max(DURATION - elapsed, 0);
    frame(t);
    if (elapsed < DURATION) {
      raf = requestAnimationFrame(tick);
    } else {
      running = false;
      const finishedForward = direction === 1;
      direction = -direction;
      const wait = finishedForward ? pauseMs : pauseFoldedMs;
      if (!disposed && wait >= 0) {
        timer = window.setTimeout(play, wait);
      }
    }
  };

  const play = () => {
    if (disposed || running) return;
    running = true;
    startedAt = performance.now();
    raf = requestAnimationFrame(tick);
  };

  if (reduced) {
    frame(DURATION);
  } else if (startOpen) {
    // Footer lockup: show the open mark immediately so spacing reads correctly,
    // then fold after the open pause.
    frame(DURATION);
    timer = window.setTimeout(play, pauseMs);
  } else {
    // Start folded, brief beat, then unfold.
    frame(0);
    timer = window.setTimeout(play, pauseFoldedMs);
  }

  return () => {
    disposed = true;
    running = false;
    cancelAnimationFrame(raf);
    clearTimeout(timer);
    host.replaceChildren();
  };
}


/* --- lockup: vectorized IBM Plex “rh1.tech” + equal gaps + contrast --- */
const RH1_WORDMARK = {
  viewBox: "59.00 -718.00 3466.00 736.00",
  dRh1: "M65.0 0.0V-496.0H185.0L195.0 -409.0H199.0Q226.0 -459.0 263.5 -483.5Q301.0 -508.0 340.0 -508.0Q361.0 -508.0 375.0 -505.5Q389.0 -503.0 399.0 -498.0L371.0 -373.0Q359.0 -376.0 348.0 -377.5Q337.0 -379.0 321.0 -379.0Q293.0 -379.0 262.5 -359.0Q232.0 -339.0 212.0 -288.0V0.0ZM511.0 0.0V-701.0H658.0V-529.0L651.0 -440.0Q679.0 -465.0 716.5 -486.5Q754.0 -508.0 804.0 -508.0Q886.0 -508.0 922.0 -454.5Q958.0 -401.0 958.0 -308.0V0.0H811.0V-289.0Q811.0 -343.0 796.5 -363.0Q782.0 -383.0 750.0 -383.0Q722.0 -383.0 702.0 -370.5Q682.0 -358.0 658.0 -335.0V0.0ZM1070.0 0.0V-119.0H1208.0V-486.0H1089.0V-577.0Q1141.0 -587.0 1177.5 -601.0Q1214.0 -615.0 1247.0 -635.0H1355.0V-119.0H1474.0V0.0Z",
  dTech: "M1626.0 12.0Q1602.0 12.0 1584.0 -5.5Q1566.0 -23.0 1566.0 -50.0Q1566.0 -79.0 1584.0 -96.5Q1602.0 -114.0 1626.0 -114.0Q1651.0 -114.0 1668.0 -96.5Q1685.0 -79.0 1685.0 -50.0Q1685.0 -23.0 1668.0 -5.5Q1651.0 12.0 1626.0 12.0ZM1982.0 9.0Q1905.0 9.0 1874.0 -36.0Q1843.0 -81.0 1843.0 -153.0V-419.0H1771.0V-481.0L1847.0 -486.0L1857.0 -642.0H1926.0V-486.0H2058.0V-419.0H1926.0V-152.0Q1926.0 -108.0 1942.5 -83.0Q1959.0 -58.0 2001.0 -58.0Q2014.0 -58.0 2028.0 -60.5Q2042.0 -63.0 2056.0 -70.0L2074.0 -12.0Q2055.0 -3.0 2031.0 3.0Q2007.0 9.0 1982.0 9.0ZM2394.0 12.0Q2329.0 12.0 2275.5 -18.5Q2222.0 -49.0 2191.0 -105.5Q2160.0 -162.0 2160.0 -243.0Q2160.0 -322.0 2192.0 -379.0Q2224.0 -436.0 2274.0 -467.0Q2324.0 -498.0 2380.0 -498.0Q2442.0 -498.0 2485.5 -469.5Q2529.0 -441.0 2551.0 -390.0Q2573.0 -339.0 2573.0 -270.0Q2573.0 -257.0 2572.5 -245.0Q2572.0 -233.0 2569.0 -221.0H2219.0V-286.0H2499.0Q2499.0 -356.0 2468.5 -393.5Q2438.0 -431.0 2381.0 -431.0Q2348.0 -431.0 2316.0 -412.5Q2284.0 -394.0 2263.5 -353.0Q2243.0 -312.0 2243.0 -244.0Q2243.0 -182.0 2264.5 -140.0Q2286.0 -98.0 2323.0 -76.5Q2360.0 -55.0 2404.0 -55.0Q2439.0 -55.0 2469.5 -65.0Q2500.0 -75.0 2525.0 -92.0L2555.0 -38.0Q2523.0 -16.0 2483.5 -2.0Q2444.0 12.0 2394.0 12.0ZM2888.0 12.0Q2823.0 12.0 2771.0 -18.0Q2719.0 -48.0 2689.0 -105.0Q2659.0 -162.0 2659.0 -242.0Q2659.0 -324.0 2691.5 -381.0Q2724.0 -438.0 2777.5 -468.0Q2831.0 -498.0 2893.0 -498.0Q2941.0 -498.0 2975.5 -482.0Q3010.0 -466.0 3035.0 -441.0L2994.0 -390.0Q2976.0 -408.0 2951.5 -418.5Q2927.0 -429.0 2896.0 -429.0Q2853.0 -429.0 2818.5 -405.5Q2784.0 -382.0 2764.0 -340.0Q2744.0 -298.0 2744.0 -242.0Q2744.0 -187.0 2763.0 -145.0Q2782.0 -103.0 2816.0 -79.5Q2850.0 -56.0 2895.0 -56.0Q2929.0 -56.0 2958.0 -69.0Q2987.0 -82.0 3008.0 -102.0L3044.0 -52.0Q3013.0 -23.0 2974.0 -5.5Q2935.0 12.0 2888.0 12.0ZM3130.0 0.0V-712.0H3212.0V-518.0L3209.0 -418.0Q3244.0 -451.0 3282.5 -474.5Q3321.0 -498.0 3371.0 -498.0Q3448.0 -498.0 3483.5 -449.5Q3519.0 -401.0 3519.0 -308.0V0.0H3437.0V-297.0Q3437.0 -366.0 3415.5 -396.5Q3394.0 -427.0 3345.0 -427.0Q3308.0 -427.0 3278.5 -408.0Q3249.0 -389.0 3212.0 -352.0V0.0Z",
  d: "M65.0 0.0V-496.0H185.0L195.0 -409.0H199.0Q226.0 -459.0 263.5 -483.5Q301.0 -508.0 340.0 -508.0Q361.0 -508.0 375.0 -505.5Q389.0 -503.0 399.0 -498.0L371.0 -373.0Q359.0 -376.0 348.0 -377.5Q337.0 -379.0 321.0 -379.0Q293.0 -379.0 262.5 -359.0Q232.0 -339.0 212.0 -288.0V0.0ZM511.0 0.0V-701.0H658.0V-529.0L651.0 -440.0Q679.0 -465.0 716.5 -486.5Q754.0 -508.0 804.0 -508.0Q886.0 -508.0 922.0 -454.5Q958.0 -401.0 958.0 -308.0V0.0H811.0V-289.0Q811.0 -343.0 796.5 -363.0Q782.0 -383.0 750.0 -383.0Q722.0 -383.0 702.0 -370.5Q682.0 -358.0 658.0 -335.0V0.0ZM1070.0 0.0V-119.0H1208.0V-486.0H1089.0V-577.0Q1141.0 -587.0 1177.5 -601.0Q1214.0 -615.0 1247.0 -635.0H1355.0V-119.0H1474.0V0.0ZM1626.0 12.0Q1602.0 12.0 1584.0 -5.5Q1566.0 -23.0 1566.0 -50.0Q1566.0 -79.0 1584.0 -96.5Q1602.0 -114.0 1626.0 -114.0Q1651.0 -114.0 1668.0 -96.5Q1685.0 -79.0 1685.0 -50.0Q1685.0 -23.0 1668.0 -5.5Q1651.0 12.0 1626.0 12.0ZM1982.0 9.0Q1905.0 9.0 1874.0 -36.0Q1843.0 -81.0 1843.0 -153.0V-419.0H1771.0V-481.0L1847.0 -486.0L1857.0 -642.0H1926.0V-486.0H2058.0V-419.0H1926.0V-152.0Q1926.0 -108.0 1942.5 -83.0Q1959.0 -58.0 2001.0 -58.0Q2014.0 -58.0 2028.0 -60.5Q2042.0 -63.0 2056.0 -70.0L2074.0 -12.0Q2055.0 -3.0 2031.0 3.0Q2007.0 9.0 1982.0 9.0ZM2394.0 12.0Q2329.0 12.0 2275.5 -18.5Q2222.0 -49.0 2191.0 -105.5Q2160.0 -162.0 2160.0 -243.0Q2160.0 -322.0 2192.0 -379.0Q2224.0 -436.0 2274.0 -467.0Q2324.0 -498.0 2380.0 -498.0Q2442.0 -498.0 2485.5 -469.5Q2529.0 -441.0 2551.0 -390.0Q2573.0 -339.0 2573.0 -270.0Q2573.0 -257.0 2572.5 -245.0Q2572.0 -233.0 2569.0 -221.0H2219.0V-286.0H2499.0Q2499.0 -356.0 2468.5 -393.5Q2438.0 -431.0 2381.0 -431.0Q2348.0 -431.0 2316.0 -412.5Q2284.0 -394.0 2263.5 -353.0Q2243.0 -312.0 2243.0 -244.0Q2243.0 -182.0 2264.5 -140.0Q2286.0 -98.0 2323.0 -76.5Q2360.0 -55.0 2404.0 -55.0Q2439.0 -55.0 2469.5 -65.0Q2500.0 -75.0 2525.0 -92.0L2555.0 -38.0Q2523.0 -16.0 2483.5 -2.0Q2444.0 12.0 2394.0 12.0ZM2888.0 12.0Q2823.0 12.0 2771.0 -18.0Q2719.0 -48.0 2689.0 -105.0Q2659.0 -162.0 2659.0 -242.0Q2659.0 -324.0 2691.5 -381.0Q2724.0 -438.0 2777.5 -468.0Q2831.0 -498.0 2893.0 -498.0Q2941.0 -498.0 2975.5 -482.0Q3010.0 -466.0 3035.0 -441.0L2994.0 -390.0Q2976.0 -408.0 2951.5 -418.5Q2927.0 -429.0 2896.0 -429.0Q2853.0 -429.0 2818.5 -405.5Q2784.0 -382.0 2764.0 -340.0Q2744.0 -298.0 2744.0 -242.0Q2744.0 -187.0 2763.0 -145.0Q2782.0 -103.0 2816.0 -79.5Q2850.0 -56.0 2895.0 -56.0Q2929.0 -56.0 2958.0 -69.0Q2987.0 -82.0 3008.0 -102.0L3044.0 -52.0Q3013.0 -23.0 2974.0 -5.5Q2935.0 12.0 2888.0 12.0ZM3130.0 0.0V-712.0H3212.0V-518.0L3209.0 -418.0Q3244.0 -451.0 3282.5 -474.5Q3321.0 -498.0 3371.0 -498.0Q3448.0 -498.0 3483.5 -449.5Q3519.0 -401.0 3519.0 -308.0V0.0H3437.0V-297.0Q3437.0 -366.0 3415.5 -396.5Q3394.0 -427.0 3345.0 -427.0Q3308.0 -427.0 3278.5 -408.0Q3249.0 -389.0 3212.0 -352.0V0.0Z",
};

const RH1_LOCKUP_CSS = `
.footer-rh1{
  display:inline-flex!important;
  align-items:center!important;
  gap:0!important;
  margin-left:0!important;
  margin-inline-start:0!important;
  vertical-align:middle;
  white-space:nowrap;
  text-decoration:none;
}
.footer-credit,.rh1-credit-line{
  --rh1-credit-gap:.4rem;
  display:inline-flex;
  align-items:center;
  gap:var(--rh1-credit-gap);
}
.rh1-lockup{
  --rh1-credit-gap:.4rem;
  display:inline-flex!important;
  align-items:center!important;
  gap:var(--rh1-credit-gap)!important;
  line-height:0;
}
.rh1-mark{
  display:inline-flex!important;
  flex-shrink:0!important;
  align-items:center;
  justify-content:center;
  width:1.125rem!important;
  height:1.125rem!important;
  min-width:1.125rem;
  min-height:1.125rem;
  line-height:0!important;
  overflow:visible!important;
}
.rh1-mark svg{
  display:block!important;
  width:100%!important;
  height:100%!important;
  overflow:visible!important;
}
.sr-only{position:absolute!important;width:1px!important;height:1px!important;padding:0!important;margin:-1px!important;overflow:hidden!important;clip:rect(0,0,0,0)!important;white-space:nowrap!important;border:0!important;}
.rh1-wordmark{
  display:block!important;
  flex-shrink:0!important;
  height:.7rem!important;
  width:auto!important;
  overflow:visible!important;
  fill:currentColor;
}
`;

function ensureRh1LockupStyles() {
  if (document.getElementById('rh1-lockup-style')) return;
  const style = document.createElement('style');
  style.id = 'rh1-lockup-style';
  style.textContent = RH1_LOCKUP_CSS;
  document.head.appendChild(style);
}

function sRGB(c) {
  const x = c / 255;
  return x <= 0.03928 ? x / 12.92 : Math.pow((x + 0.055) / 1.055, 2.4);
}

function parseCssColor(str) {
  if (!str || str === 'transparent') return null;
  const m = str.match(/rgba?\(\s*([\d.]+)\s*,\s*([\d.]+)\s*,\s*([\d.]+)(?:\s*[\/,]\s*([\d.]+%?))?\s*\)/i);
  if (m) {
    const a = m[4] === undefined ? 1 : (String(m[4]).endsWith('%') ? Number(m[4]) / 100 : Number(m[4]));
    if (a < 0.4) return null;
    return { r: Number(m[1]), g: Number(m[2]), b: Number(m[3]), a };
  }
  const hex = str.trim().match(/^#([0-9a-f]{3}|[0-9a-f]{6}|[0-9a-f]{8})$/i);
  if (hex) {
    let h = hex[1];
    if (h.length === 3) h = h.split('').map((c) => c + c).join('');
    const r = parseInt(h.slice(0, 2), 16);
    const g = parseInt(h.slice(2, 4), 16);
    const b = parseInt(h.slice(4, 6), 16);
    const a = h.length === 8 ? parseInt(h.slice(6, 8), 16) / 255 : 1;
    if (a < 0.4) return null;
    return { r, g, b, a };
  }
  return null;
}

/** Dark ink on light surfaces, light ink on dark — keeps “rh1.tech” readable. */
function readableInkFor(el) {
  let node = el;
  while (node && node.nodeType === 1) {
    const bg = parseCssColor(getComputedStyle(node).backgroundColor);
    if (bg) {
      const L = 0.2126 * sRGB(bg.r) + 0.7152 * sRGB(bg.g) + 0.0722 * sRGB(bg.b);
      return L < 0.45 ? '#f2f2f2' : '#1a1a1a';
    }
    node = node.parentElement;
  }
  try {
    if (matchMedia('(prefers-color-scheme: dark)').matches) return '#f2f2f2';
  } catch (_) {}
  return '#1a1a1a';
}

function createRh1WordmarkEl() {
  const svg = document.createElementNS(SVG_NS, 'svg');
  svg.setAttribute('class', 'rh1-wordmark');
  svg.setAttribute('viewBox', RH1_WORDMARK.viewBox);
  svg.setAttribute('aria-hidden', 'true');
  svg.setAttribute('focusable', 'false');
  for (const d of [RH1_WORDMARK.dRh1, RH1_WORDMARK.dTech]) {
    const path = document.createElementNS(SVG_NS, 'path');
    path.setAttribute('d', d);
    svg.appendChild(path);
  }
  return svg;
}

function stripRh1TechText(link) {
  const walk = (root) => {
    for (const node of Array.from(root.childNodes)) {
      if (node.nodeType === Node.TEXT_NODE) {
        const raw = node.textContent || '';
        if (/rh1\.tech/i.test(raw)) {
          const cleaned = raw.replace(/\s*rh1\.tech\s*/gi, ' ').replace(/\s+/g, ' ');
          if (!cleaned.trim()) node.parentNode && node.parentNode.removeChild(node);
          else node.textContent = cleaned;
        }
      } else if (node.nodeType === 1 && node.matches && !node.matches('.rh1-lockup, .rh1-mark, .rh1-wordmark, svg, path')) {
        if (node.matches('.sr-only, span.sr-only')) continue;
        if (/^\s*rh1\.tech\s*$/i.test(node.textContent || '')) {
          node.parentNode && node.parentNode.removeChild(node);
        } else {
          walk(node);
        }
      }
    }
  };
  walk(link);
  if (!link.getAttribute('aria-label')) link.setAttribute('aria-label', 'rh1.tech');
  link.querySelectorAll(':scope > .sr-only, :scope > span.sr-only').forEach((s) => {
    if (/^\s*rh1\.tech\s*$/i.test(s.textContent || '')) s.remove();
  });
}

function enhanceFooterRh1(link, id) {
  if (!(link instanceof HTMLAnchorElement)) return;
  link.classList.add('footer-rh1');
  ensureRh1LockupStyles();

  let lockup = link.querySelector(':scope > .rh1-lockup');
  let mark = link.querySelector('.rh1-mark, [data-rh1-mark]');
  if (!lockup) {
    lockup = document.createElement('span');
    lockup.className = 'rh1-lockup';
    if (mark) {
      mark.before(lockup);
      lockup.appendChild(mark);
    } else {
      mark = document.createElement('span');
      mark.className = 'rh1-mark';
      mark.setAttribute('data-rh1-mark', '');
      mark.setAttribute('aria-hidden', 'true');
      lockup.appendChild(mark);
      link.insertBefore(lockup, link.firstChild);
    }
  } else if (mark && mark.parentElement !== lockup) {
    lockup.appendChild(mark);
  }

  if (!lockup.querySelector('.rh1-wordmark')) {
    lockup.appendChild(createRh1WordmarkEl());
  }

  stripRh1TechText(link);

  // Equal gap: Matveev text → lockup. If previous sibling is text/name, wrap credit line.
  const parent = link.parentElement;
  if (parent && !parent.classList.contains('footer-credit') && !parent.classList.contains('rh1-credit-line')) {
    // Prefer not to restructure whole footers; instead margin on lockup side is via lockup gap only.
    // Put the same gap before the lockup via margin-inline-start when preceded by text.
    const prev = link.previousSibling;
    if (prev && (prev.nodeType === Node.TEXT_NODE || (prev.nodeType === 1 && !prev.classList?.contains('rh1-lockup')))) {
      link.style.marginInlineStart = '';
      // Only promote simple credit wraps — never whole footer columns (antega etc.)
      const simpleCredit =
        parent.matches('p, span, .credit, .footer-credit, .footer-credit-name') ||
        (parent.childNodes.length <= 3 && !parent.querySelector('div, br, img, ul, section, .column'));
      if (simpleCredit) parent.classList.add('rh1-credit-line');
    }
  }

  // Drop leftover spaces so flex gap is the only spacing Matveev ↔ mark.
  const prev = link.previousSibling;
  if (prev && prev.nodeType === Node.TEXT_NODE) {
    prev.textContent = (prev.textContent || '').replace(/\s+$/, '');
  }
  const next = link.nextSibling;
  if (next && next.nodeType === Node.TEXT_NODE && /^\s*rh1\.tech/i.test(next.textContent || '')) {
    next.textContent = (next.textContent || '').replace(/^\s*rh1\.tech\s*/i, '');
  }

  const ink = readableInkFor(link);
  link.style.setProperty('color', ink, 'important');
  lockup.style.setProperty('color', ink, 'important');
  const wm = lockup.querySelector('.rh1-wordmark');
  if (wm) wm.style.setProperty('color', ink, 'important');

  if (mark instanceof HTMLElement && mark.dataset.mounted !== '1') {
    mark.dataset.mounted = '1';
    mountRh1Mark(mark, {
      size: Number(mark.dataset.size) || 18,
      pauseMs: Number(mark.dataset.pauseMs ?? mark.dataset.pause ?? 3000),
      pauseFoldedMs: Number(mark.dataset.pauseFoldedMs ?? 200),
      id: id ?? 1,
      startOpen: true,
    });
  }
}

function autoMountRh1Marks() {
  ensureRh1LockupStyles();
  // Prefer footer links
  document.querySelectorAll('a.footer-rh1, a[href="https://rh1.tech"], a[href="https://rh1.tech/"]').forEach((a, i) => {
    if (!(a instanceof HTMLAnchorElement)) return;
    // Only credit-style links that already have a mark or sit next to Matveev / copyright
    const hasMark = a.querySelector('.rh1-mark, [data-rh1-mark]');
    const nearCredit = /Matveev|Матвеев|©/i.test((a.parentElement && a.parentElement.textContent) || '');
    if (hasMark || nearCredit || a.classList.contains('footer-rh1')) {
      enhanceFooterRh1(a, i + 1);
    }
  });
  // Orphan marks (not yet in a footer-rh1)
  document.querySelectorAll('.rh1-mark[data-rh1-mark], [data-rh1-mark]').forEach((el, i) => {
    if (!(el instanceof HTMLElement) || el.dataset.mounted === '1' || el.querySelector('svg')) return;
    const inFooter = el.closest('a.footer-rh1, a[href="https://rh1.tech"], a[href="https://rh1.tech/"]');
    if (inFooter) return; // handled above
    el.dataset.mounted = '1';
    mountRh1Mark(el, {
      size: Number(el.dataset.size) || 18,
      pauseMs: Number(el.dataset.pauseMs ?? el.dataset.pause ?? 3000),
      pauseFoldedMs: Number(el.dataset.pauseFoldedMs ?? 200),
      id: 100 + i,
      startOpen: false,
    });
  });
}

if (typeof document !== 'undefined') {
  if (document.readyState === 'loading') document.addEventListener('DOMContentLoaded', autoMountRh1Marks);
  else autoMountRh1Marks();
}

// UMD-ish globals for sites that call mount directly
if (typeof window !== 'undefined') {
  window.mountRh1Mark = mountRh1Mark;
  window.createRh1Mark = createRh1Mark;
  window.enhanceFooterRh1 = enhanceFooterRh1;
}
