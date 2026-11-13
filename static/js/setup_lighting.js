// Client-side logic for the Lighting page: the fixtures of a field profile, the scene grid, the live and preview
// strips, Test and Identify, and export and import. Every strip, swatch and preview is drawn with LedPatterns, the
// renderer the devices follow, on the server's clock.

const WHITE = "#ffffff";
const BLACK = "#000000";
const SWATCH_PIXELS = 24;     // LEDs drawn in a swatch of the grid.
const EDITOR_PIXELS = 60;     // LEDs drawn in the cell editor's preview.
const FRAME_MS = 40;          // About 25 frames a second is plenty for a preview.
const LEVEL_BLINK_MS = 1000;  // The FMS blinks digital, relay and pwm outputs at this period by default.
const STRIP_PATTERNS = ["off", "solid", "blink", "pulse", "chase", "rainbow", "progress"];
const LEVEL_PATTERNS = {
  digital: ["off", "solid", "blink"], relay: ["off", "solid", "blink"], pwm: ["off", "solid", "blink", "progress"],
};
const PATTERNS = {
  off: {name: "Off", icon: "bi-circle"},
  solid: {name: "Solid", icon: "bi-circle-fill"},
  blink: {name: "Blink", icon: "bi-lightning-fill"},
  pulse: {name: "Pulse", icon: "bi-soundwave"},
  chase: {name: "Chase", icon: "bi-arrow-right"},
  rainbow: {name: "Rainbow", icon: "bi-rainbow"},
  progress: {name: "Progress", icon: "bi-hourglass-split"},
};
const KINDS = {
  led_strip: {name: "LED strip", icon: "bi-lightbulb"},
  digital: {name: "Light", icon: "bi-lightbulb-fill"},
  relay: {name: "Relay", icon: "bi-toggle-on"},
  pwm: {name: "Dimmer", icon: "bi-brightness-high"},
};
const PALETTE = ["#ff0000", "#ff8000", "#ffff00", "#00ff00", "#00ffff", "#0000ff", "#8000ff", "#ff00ff", WHITE];
const SEGMENT_COLORS = ["#607d8b", "#8d6e63", "#7e57c2", "#26a69a", "#ef6c00", "#5c6bc0"];
const FILL_PRESETS = [
  ["Time left in the phase", "phase_remaining / phase_duration"],
  ["Time gone in the phase", "phase_elapsed / phase_duration"],
  ["Half", "0.5"],
];

const state = {
  data: null,           // Everything about the profile shown, from /setup/lighting/data.
  profile: null,        // The profile shown; null for the active one.
  live: null,           // What the hardware shows now, from the feed.
  cells: {},            // Live values of the cells' expressions: moment -> target -> {v, when, error}.
  preview: null,        // What the hardware would show at the previewed moment.
  view: "live",         // "live" or "preview".
  previewMoment: "idle",
  clockOffset: 0,       // Server clock minus browser clock, in milliseconds.
  revision: -1,         // The revision of the active configuration last loaded.
};
let websocket = null;
let painters = [];

// ---------------------------------------------------------------------------------------------------------------
// Helpers

const fieldTime = function () {
  return Date.now() + state.clockOffset;
};

const syncClock = function (serverTime) {
  if (typeof serverTime === "number") {
    state.clockOffset = serverTime - Date.now();
  }
};

const profileQuery = function () {
  return "?profile=" + encodeURIComponent(state.data ? state.data.profile : (state.profile || ""));
};

const showToast = function (message, kind) {
  $("#toast").removeClass("text-bg-danger text-bg-warning text-bg-success")
    .addClass(kind ? "text-bg-" + kind : "");
  $("#toastBody").text(message);
  bootstrap.Toast.getOrCreateInstance(document.getElementById("toast"), {delay: 5000}).show();
};

// postJson posts a JSON body and calls done(error, response); error is the server's {error, problems}.
const postJson = function (url, body, done) {
  $.ajax({
    url: url, type: "POST", contentType: "application/json", dataType: "json",
    data: body === undefined ? "" : JSON.stringify(body),
  }).done(function (response) {
    done(null, response);
  }).fail(function (xhr) {
    done(xhr.responseJSON || {error: xhr.statusText || "The request failed.", problems: []}, null);
  });
};

const allianceById = function (id) {
  return state.data.game.alliances.find(function (alliance) { return alliance.id === id; }) || null;
};

const allianceColor = function (id) {
  const alliance = allianceById(id);
  return alliance ? alliance.color.toLowerCase() : null;
};

const winnerColor = function () {
  return (state.live && state.live.winner && allianceColor(state.live.winner)) || WHITE;
};

// resolveColor turns a colour of a look into #rrggbb, for LEDs of the given alliance.
const resolveColor = function (token, alliance) {
  switch (token) {
    case "alliance":
      return allianceColor(alliance) || WHITE;
    case "winner":
      return winnerColor();
    case "off":
      return BLACK;
  }
  return token;
};

const grayHex = function (level) {
  const value = Math.round(Math.max(0, Math.min(1, level)) * 255).toString(16).padStart(2, "0");
  return "#" + value + value + value;
};

const momentById = function (id) {
  return state.data.moments.find(function (moment) { return moment.id === id; }) || null;
};

const momentName = function (id) {
  const moment = momentById(id);
  return moment ? moment.name : id;
};

const deviceById = function (id) {
  return state.data.devices.find(function (device) { return device.id === id; }) || null;
};

const brightnessOf = function (fixture) {
  return fixture.brightness === undefined || fixture.brightness === null ? 1 : fixture.brightness;
};

const cellLook = function (moment, target) {
  return (state.data.config.scenes[moment] || {})[target] || null;
};

// targetsOf returns the columns of a fixture in the grid: the whole fixture, then each segment.
const targetsOf = function (fixture) {
  const targets = [{
    id: fixture.id, fixture: fixture, segment: null, alliance: fixture.alliance || "",
    pixels: Math.max(1, fixture.pixels || 1),
  }];
  if (fixture.kind === "led_strip") {
    (fixture.segments || []).forEach(function (segment) {
      targets.push({
        id: fixture.id + "/" + segment.id, fixture: fixture, segment: segment,
        alliance: segment.alliance || fixture.alliance || "", pixels: Math.max(1, segment.count),
      });
    });
  }
  return targets;
};

const targetById = function (id) {
  for (const fixture of state.data.config.fixtures) {
    const target = targetsOf(fixture).find(function (candidate) { return candidate.id === id; });
    if (target) {
      return target;
    }
  }
  return null;
};

const targetName = function (target) {
  if (!target.segment) {
    return target.fixture.name;
  }
  return target.fixture.name + " › " + (target.segment.name || target.segment.id);
};

// fixtureRanges divides a strip into its segments and the gaps around them, each with what "alliance" means there.
const fixtureRanges = function (fixture) {
  const pixels = Math.max(1, fixture.pixels || 1);
  const segments = (fixture.segments || []).slice().sort(function (a, b) { return a.start - b.start; });
  const ranges = [];
  let next = 0;
  segments.forEach(function (segment) {
    const start = Math.max(next, segment.start);
    const end = Math.min(pixels, segment.start + segment.count);
    if (start > next) {
      ranges.push({start: next, count: start - next, alliance: fixture.alliance || ""});
    }
    if (end > start) {
      ranges.push({start: start, count: end - start, alliance: segment.alliance || fixture.alliance || ""});
      next = end;
    }
  });
  if (next < pixels) {
    ranges.push({start: next, count: pixels - next, alliance: fixture.alliance || ""});
  }
  return ranges;
};

const usesAlliance = function (look) {
  return (look.c || []).indexOf("alliance") >= 0;
};

// lookFill is the fill a progress look shows in the page: its number, the live value of its expression, or a sample.
const lookFill = function (look, moment, target) {
  if ((look.p || "solid") !== "progress") {
    return undefined;
  }
  const text = String(look.v || "").trim();
  if (text !== "" && isFinite(Number(text))) {
    return Number(text);
  }
  const value = ((state.cells[moment] || {})[target] || {}).v;
  return typeof value === "number" ? value : 0.6;
};

// lookCommand builds the command that draws a look on a target, scaled down to the given number of LEDs, the way
// the engine resolves its colours: a look of the whole fixture shows "alliance" in each segment's own colour.
const lookCommand = function (look, target, pixels, fill) {
  const fixture = target.fixture;
  const pattern = look.p || "solid";
  const brightness = (look.br === undefined || look.br === null ? 1 : look.br) * brightnessOf(fixture);
  const command = {p: pattern, ms: look.ms || 0, br: brightness};
  if (fill !== undefined) {
    command.v = fill;
  }
  if (!target.segment && usesAlliance(look) && (fixture.segments || []).length > 0) {
    const scale = pixels / Math.max(1, fixture.pixels);
    command.seg = [];
    fixtureRanges(fixture).forEach(function (range) {
      const start = Math.round(range.start * scale);
      const end = Math.round((range.start + range.count) * scale);
      if (end > start) {
        command.seg.push({
          s: start, n: end - start, p: pattern, ms: look.ms || 0, v: fill,
          c: (look.c || []).map(function (token) { return resolveColor(token, range.alliance); }),
        });
      }
    });
    return command;
  }
  command.c = (look.c || []).map(function (token) { return resolveColor(token, target.alliance); });
  return command;
};

// levelCommand is how the page draws a digital, relay or pwm fixture showing a look.
const levelCommand = function (fixture, look, fill) {
  const pattern = look.p || "solid";
  if (pattern === "off") {
    return {v: 0};
  }
  let high = 1;
  if (fixture.kind === "pwm") {
    high = brightnessOf(fixture) * (look.br === undefined || look.br === null ? 1 : look.br);
    if (pattern === "progress") {
      high *= fill === undefined ? 0.6 : fill;
    }
  }
  if (pattern === "blink") {
    return {p: "blink", c: [grayHex(high)], ms: look.ms || LEVEL_BLINK_MS};
  }
  return {v: high};
};

const isAnimated = function (look) {
  const pattern = look.p || "solid";
  return pattern === "blink" || pattern === "pulse" || pattern === "chase" || pattern === "rainbow" ||
    (pattern === "progress" && !isFinite(Number(String(look.v || "").trim())));
};

// ---------------------------------------------------------------------------------------------------------------
// Drawing

const fitCanvas = function (canvas) {
  const ratio = window.devicePixelRatio || 1;
  const width = Math.max(1, Math.round(canvas.clientWidth * ratio));
  const height = Math.max(1, Math.round(canvas.clientHeight * ratio));
  if (canvas.width !== width || canvas.height !== height) {
    canvas.width = width;
    canvas.height = height;
  }
  return canvas.getContext("2d");
};

const rgb = function (color, alpha) {
  return "rgba(" + Math.round(color[0]) + "," + Math.round(color[1]) + "," + Math.round(color[2]) + "," + alpha + ")";
};

// drawLeds draws a row of LEDs, each [r, g, b], with a little glow around the lit ones.
const drawLeds = function (canvas, colors, alpha) {
  const context = fitCanvas(canvas);
  const width = canvas.width;
  const height = canvas.height;
  context.globalAlpha = 1;
  context.fillStyle = "#0d0f12";
  context.fillRect(0, 0, width, height);
  context.globalAlpha = alpha === undefined ? 1 : alpha;
  const count = colors.length;
  const step = width / count;
  const gap = step >= 5 ? Math.max(1, Math.floor(step * 0.2)) : 0;
  const pad = Math.max(1, Math.round(height * 0.14));
  const box = function (i) {
    const x = Math.floor(i * step);
    return [x + Math.floor(gap / 2), Math.max(1, Math.floor((i + 1) * step) - x - gap)];
  };
  for (let i = 0; i < count; i++) {
    const color = colors[i];
    if (color[0] + color[1] + color[2] > 0) {
      const [x, w] = box(i);
      context.fillStyle = rgb(color, 0.28);
      context.fillRect(x - gap, 0, w + 2 * gap, height);
    }
  }
  for (let i = 0; i < count; i++) {
    const color = colors[i];
    const [x, w] = box(i);
    context.fillStyle = color[0] + color[1] + color[2] > 0 ? rgb(color, 1) : "#23272d";
    context.fillRect(x, pad, w, height - 2 * pad);
  }
  context.globalAlpha = 1;
};

// drawLamp draws a single light: a digital, relay or pwm output.
const drawLamp = function (canvas, color, alpha) {
  const context = fitCanvas(canvas);
  const size = Math.min(canvas.width, canvas.height);
  const radius = size / 2;
  context.clearRect(0, 0, canvas.width, canvas.height);
  context.globalAlpha = alpha === undefined ? 1 : alpha;
  const lit = color[0] + color[1] + color[2] > 0;
  const gradient = context.createRadialGradient(radius, radius, radius * 0.1, radius, radius, radius);
  gradient.addColorStop(0, lit ? rgb(color, 1) : "#2a2e34");
  gradient.addColorStop(0.7, lit ? rgb(color, 0.85) : "#1d2025");
  gradient.addColorStop(1, lit ? rgb(color, 0.15) : "#15181c");
  context.fillStyle = gradient;
  context.beginPath();
  context.arc(canvas.width / 2, canvas.height / 2, radius, 0, 2 * Math.PI);
  context.fill();
  context.globalAlpha = 1;
};

// paintLook draws a look of a target into a canvas: a swatch, or the cell editor's preview.
const paintLook = function (canvas, target, look, moment, maxPixels, t, alpha) {
  if (!look) {
    if (target.fixture.kind === "led_strip") {
      drawLeds(canvas, LedPatterns.render(null, Math.min(target.pixels, maxPixels), t), alpha);
    } else {
      drawLamp(canvas, [0, 0, 0], alpha);
    }
    return;
  }
  const fill = lookFill(look, moment, target.id);
  if (target.fixture.kind !== "led_strip") {
    drawLamp(canvas, LedPatterns.renderSingle(levelCommand(target.fixture, look, fill), t), alpha);
    return;
  }
  const pixels = Math.min(target.pixels, maxPixels);
  drawLeds(canvas, LedPatterns.render(lookCommand(look, target, pixels, fill), pixels, t), alpha);
};

// addPainter has a canvas drawn now and, while animated() says so, on every frame. Painters whose canvas has left
// the page are dropped.
const addPainter = function (canvas, paint, animated) {
  painters.push({canvas: canvas, paint: paint, animated: animated || function () { return false; }, dirty: true});
};

let lastFrame = 0;
const frame = function (now) {
  requestAnimationFrame(frame);
  if (now - lastFrame < FRAME_MS) {
    return;
  }
  lastFrame = now;
  const t = fieldTime();
  painters = painters.filter(function (painter) { return painter.canvas.isConnected; });
  painters.forEach(function (painter) {
    if (painter.dirty || painter.animated()) {
      painter.dirty = false;
      painter.paint(t);
    }
  });
};

const repaintAll = function () {
  painters.forEach(function (painter) { painter.dirty = true; });
};

// ---------------------------------------------------------------------------------------------------------------
// Loading

const loadData = function () {
  const query = state.profile ? "?profile=" + encodeURIComponent(state.profile) : "";
  $.getJSON("/setup/lighting/data" + query).done(function (data) {
    state.data = data;
    state.profile = data.profile;
    if (data.active) {
      state.revision = data.status.revision;
    }
    syncClock(data.time);
    $("#loadError").addClass("d-none");
    render();
  }).fail(function (xhr) {
    $("#loadError").removeClass("d-none")
      .text("The lighting could not be loaded: " + ((xhr.responseJSON || {}).error || xhr.statusText));
  });
};

const render = function () {
  renderHeader();
  renderStrips();
  renderFixtures();
  renderGrid();
  renderProblems();
  updateMoment();
};

// ---------------------------------------------------------------------------------------------------------------
// Header, live strips and fixtures

const renderHeader = function () {
  const data = state.data;
  const select = $("#profileSelect").empty();
  data.profiles.forEach(function (profile) {
    select.append($("<option>").val(profile.name).text(profile.name + (profile.active ? " (active)" : "")));
  });
  select.val(data.profile);
  $("#gameName").text(data.game.name);
  $("#exportButton").attr("href", "/setup/lighting/export" + profileQuery());

  const active = data.profiles.find(function (profile) { return profile.active; });
  const notice = $("#profileNotice").toggleClass("d-none", data.active).empty();
  if (!data.active) {
    notice.append(document.createTextNode("You are editing the lighting of "), $("<strong>").text(data.profile),
      document.createTextNode(". The field uses " + (active ? active.name : "another profile") +
        "; choose the active profile on the "), $("<a href='/setup/settings'>").text("Settings page"),
      document.createTextNode(". The live view, Preview and Test show the active profile."));
  }

  [$("#previewMoment"), $("#testMoment")].forEach(function (momentSelect) {
    const value = momentSelect.val();
    momentSelect.empty();
    data.moments.forEach(function (moment) {
      momentSelect.append($("<option>").val(moment.id).text(moment.name).attr("title", moment.when));
    });
    momentSelect.val(value && momentById(value) ? value : "idle");
  });
  state.previewMoment = $("#previewMoment").val();
  const hasFixtures = data.config.fixtures.length > 0;
  $("#testButton, #testMoment").prop("disabled", !data.active || !hasFixtures);
  $("#viewLive, #viewPreview").prop("disabled", !data.active);
};

const onlineDot = function (device) {
  const online = device && device.online;
  return $("<span class='online-dot'>").toggleClass("online", !!online)
    .attr("title", !device ? "Not connected yet" : online ? "Online" : "Offline");
};

const renderStrips = function () {
  const data = state.data;
  const container = $("#strips").empty();
  const fixtures = data.config.fixtures;
  $("#stripsEmpty").toggleClass("d-none", fixtures.length > 0 || !data.active);
  $("#stripsInactive").toggleClass("d-none", data.active)
    .text("The field shows the active profile. Its fixtures appear here when you switch to it.");
  if (!data.active) {
    return;
  }
  fixtures.forEach(function (fixture) {
    const row = $("<div class='strip-row'>").attr("data-fixture", fixture.id);
    const device = deviceById(fixture.device);
    const name = $("<div class='strip-name'>").append(onlineDot(device), $("<span>").text(fixture.name),
      $("<small>").text(KINDS[fixture.kind] ? KINDS[fixture.kind].name : fixture.kind));
    const view = $("<div class='strip-view'>");
    const canvas = $("<canvas class='strip-canvas'>").toggleClass("lamp", fixture.kind !== "led_strip")[0];
    view.append(canvas);
    if (fixture.kind === "led_strip" && (fixture.segments || []).length > 0) {
      const ticks = $("<div class='strip-ticks'>");
      const pixels = Math.max(1, fixture.pixels);
      fixture.segments.forEach(function (segment) {
        ticks.append($("<span>").text(segment.name || segment.id).attr("title", "LEDs " + segment.start + "–" +
          (segment.start + segment.count - 1)).css({
          left: (segment.start / pixels * 100) + "%", width: (segment.count / pixels * 100) + "%",
        }));
      });
      view.append(ticks);
    }
    const note = stripNote(fixture);
    if (note) {
      view.append($("<div class='strip-note'>").text(note));
    }
    const identify = $("<button type='button' class='btn btn-outline-secondary btn-sm'>")
      .attr("title", "Identify: the device shows itself and the fixture blinks white for 3 seconds")
      .append($("<i class='bi bi-eye'>"), " Identify")
      .on("click", function () { identifyFixture(fixture); });
    row.append(name, view, $("<div class='strip-actions'>").append(identify));
    container.append(row);
    addPainter(canvas, function (t) { paintStrip(canvas, fixture, t); }, function () { return true; });
  });
};

// stripNote says why a fixture is not lit from here, if it is not.
const stripNote = function (fixture) {
  const status = state.data.status;
  if (status.conflicts && status.conflicts[fixture.id]) {
    return "Not driven: " + status.conflicts[fixture.id];
  }
  if (status.claimed && !status.claimed[fixture.id]) {
    return "Not driven: the fixture has errors";
  }
  return "";
};

const stripCommand = function (fixture) {
  if (state.view === "preview") {
    return state.preview && state.preview.fixtures ? state.preview.fixtures[fixture.id] || null : null;
  }
  return state.live && state.live.fixtures ? state.live.fixtures[fixture.id] || null : null;
};

// paintStrip draws what a fixture shows, LED 0 of the fixture on the left: a reversed strip counts from its far end.
const paintStrip = function (canvas, fixture, t) {
  const command = stripCommand(fixture);
  if (fixture.kind !== "led_strip") {
    drawLamp(canvas, LedPatterns.renderSingle(command, t));
    return;
  }
  let colors = LedPatterns.render(command, Math.max(1, fixture.pixels), t);
  if (fixture.reverse) {
    colors = colors.slice().reverse();
  }
  drawLeds(canvas, colors);
};

const renderFixtures = function () {
  const data = state.data;
  const list = $("#fixtureList").empty();
  $("#fixturesEmpty").toggleClass("d-none", data.config.fixtures.length > 0);
  data.config.fixtures.forEach(function (fixture) {
    const device = deviceById(fixture.device);
    const color = allianceColor(fixture.alliance);
    const card = $("<div class='fixture-card'>").css("border-left-color", color || "");
    const kind = KINDS[fixture.kind] || {name: fixture.kind, icon: "bi-question-circle"};
    const edit = $("<button type='button' class='btn btn-outline-primary btn-sm'>")
      .append($("<i class='bi bi-pencil'>"))
      .attr("title", "Edit").on("click", function () { openFixtureEditor(fixture); });
    const identify = $("<button type='button' class='btn btn-outline-secondary btn-sm'>")
      .append($("<i class='bi bi-eye'>")).attr("title", "Identify")
      .prop("disabled", !data.active).on("click", function () { identifyFixture(fixture); });
    card.append($("<div class='title'>").append($("<i class='bi'>").addClass(kind.icon), onlineDot(device),
      $("<strong>").text(fixture.name).attr("title", fixture.name), identify, edit));

    const meta = [kind.name + (fixture.kind === "led_strip" ? ", " + fixture.pixels + " LEDs" : ""),
      (device ? device.name : fixture.device) + " · " + fixture.channel];
    card.append($("<div class='meta'>").text(meta.join(" — ")));
    const chips = $("<div>");
    if (fixture.alliance) {
      chips.append($("<span class='chip'>").append($("<span class='swatch-dot'>").css("background", color || WHITE),
        (allianceById(fixture.alliance) || {name: fixture.alliance}).name));
    }
    if (brightnessOf(fixture) < 1) {
      chips.append($("<span class='chip'>").text("Brightness " + Math.round(brightnessOf(fixture) * 100) + "%"));
    }
    if (fixture.reverse) {
      chips.append($("<span class='chip'>").text("Reversed"));
    }
    (fixture.segments || []).forEach(function (segment) {
      const segmentColor = allianceColor(segment.alliance);
      const chip = $("<span class='chip'>").attr("title", "LEDs " + segment.start + "–" +
        (segment.start + segment.count - 1));
      if (segmentColor) {
        chip.append($("<span class='swatch-dot'>").css("background", segmentColor));
      }
      chips.append(chip.append(document.createTextNode(segment.name || segment.id)));
    });
    card.append(chips);
    const note = data.active ? stripNote(fixture) : "";
    const problems = data.problems.filter(function (problem) {
      return problem.fixture === fixture.id && !problem.moment;
    });
    if (note) {
      card.append($("<div class='small text-warning mt-1'>").text(note));
    }
    problems.forEach(function (problem) {
      card.append($("<div class='small mt-1'>").addClass(problem.warning ? "text-warning" : "text-danger")
        .text(problem.message));
    });
    list.append(card);
  });
};

// ---------------------------------------------------------------------------------------------------------------
// The scene grid

const cellProblems = function (moment, target) {
  return state.data.problems.filter(function (problem) {
    return problem.moment === moment && problem.target === target;
  });
};

// inheritedLook returns what an empty cell falls through to, as configured: for a segment, the whole fixture's look
// at the same moment, then the looks further down.
const inheritedLook = function (index, target) {
  const moments = state.data.moments;
  if (target.segment) {
    const look = cellLook(moments[index].id, target.fixture.id);
    if (look) {
      return {look: look, moment: moments[index]};
    }
  }
  for (let i = index + 1; i < moments.length; i++) {
    const look = cellLook(moments[i].id, target.id) ||
      (target.segment ? cellLook(moments[i].id, target.fixture.id) : null);
    if (look) {
      return {look: look, moment: moments[i]};
    }
  }
  return null;
};

const renderGrid = function () {
  const data = state.data;
  const table = $("#sceneGrid").empty();
  const fixtures = data.config.fixtures;
  if (fixtures.length === 0) {
    table.append($("<tbody>").append($("<tr>").append(
      $("<td class='text-secondary p-3'>").text("Add a fixture to set its scenes."))));
    return;
  }
  const split = fixtures.some(function (fixture) { return targetsOf(fixture).length > 1; });
  const head = $("<thead>");
  const top = $("<tr>").append($("<th class='moment-col'>").attr("rowspan", split ? 2 : 1).text("Moment"));
  const bottom = $("<tr>");
  fixtures.forEach(function (fixture) {
    const targets = targetsOf(fixture);
    const kind = KINDS[fixture.kind] || {icon: "bi-question-circle"};
    const header = $("<th class='fixture-head'>").attr("colspan", targets.length)
      .append($("<i class='bi me-1'>").addClass(kind.icon), document.createTextNode(fixture.name))
      .attr("title", fixture.device + " / " + fixture.channel);
    if (targets.length === 1 && split) {
      header.attr("rowspan", 2);
    }
    top.append(header);
    if (targets.length > 1) {
      targets.forEach(function (target, i) {
        bottom.append($("<th>").toggleClass("fixture-head", i === 0)
          .text(target.segment ? target.segment.name || target.segment.id : "Whole"));
      });
    }
  });
  head.append(top);
  if (split) {
    head.append(bottom);
  }

  const body = $("<tbody>");
  let lastKind = null;
  data.moments.forEach(function (moment, index) {
    const row = $("<tr>").attr("data-moment", moment.id).toggleClass("group-start", moment.kind !== lastKind);
    lastKind = moment.kind;
    const label = $("<div class='moment-name'>");
    label.append($("<span class='name'>").text(moment.name).attr("title", moment.when + " · click to preview")
      .on("click", function () { previewMoment(moment.id); }));
    if (moment.kind !== "field") {
      label.append($("<span class='moment-kind'>").text(moment.kind));
    }
    if (data.active) {
      label.append($("<button type='button' class='btn btn-link btn-sm p-0 text-warning'>")
        .attr("title", "Test " + moment.name + " on the field for 10 seconds")
        .append($("<i class='bi bi-broadcast'>"))
        .on("click", function () { startTest(moment.id); }));
    }
    row.append($("<th class='moment-col'>").append(label));
    fixtures.forEach(function (fixture) {
      targetsOf(fixture).forEach(function (target, i) {
        row.append(gridCell(moment, index, target).toggleClass("first-of-fixture", i === 0));
      });
    });
    body.append(row);
  });
  table.append(head, body);
  highlightRows();
};

const gridCell = function (moment, index, target) {
  const look = cellLook(moment.id, target.id);
  const cell = $("<td class='scene-cell' tabindex='0'>").attr({"data-moment": moment.id, "data-target": target.id});
  const canvas = $("<canvas class='swatch'>").toggleClass("lamp", target.fixture.kind !== "led_strip")[0];
  cell.append(canvas);
  if (look) {
    cell.addClass("set").attr("title", target.id + " at " + moment.name + ": " + describeLook(look, target));
    if (look.when) {
      const live = (state.cells[moment.id] || {})[target.id];
      cell.append($("<span class='cell-flag'>").text("if").toggleClass("off", !!live && !live.when)
        .attr("title", "Only while " + look.when));
    }
    addPainter(canvas, function (t) { paintLook(canvas, target, look, moment.id, SWATCH_PIXELS, t, 1); },
      function () { return isAnimated(look); });
  } else {
    const inherited = inheritedLook(index, target);
    cell.addClass("empty").attr("title", inherited ? "Empty: falls through to " + inherited.moment.name :
      "Empty: off unless a moment further down has a look");
    addPainter(canvas, function (t) {
      paintLook(canvas, target, inherited ? inherited.look : null, inherited ? inherited.moment.id : moment.id,
        SWATCH_PIXELS, t, 0.3);
    }, function () { return !!inherited && isAnimated(inherited.look); });
  }
  const problems = cellProblems(moment.id, target.id);
  if (problems.length > 0) {
    const error = problems.some(function (problem) { return !problem.warning; });
    cell.addClass(error ? "has-error" : "has-warning")
      .attr("title", problems.map(function (problem) { return problem.message; }).join("\n"));
  }
  cell.on("click keydown", function (event) {
    if (event.type === "keydown" && event.key !== "Enter" && event.key !== " ") {
      return;
    }
    event.preventDefault();
    openCellEditor(moment.id, target.id);
  });
  return cell;
};

const describeLook = function (look, target) {
  const pattern = PATTERNS[look.p || "solid"] ? PATTERNS[look.p || "solid"].name : look.p;
  const parts = [pattern];
  if (target.fixture.kind === "led_strip" && (look.c || []).length > 0) {
    parts.push(look.c.join(" / "));
  }
  if (look.ms) {
    parts.push(look.ms + " ms");
  }
  if (look.br !== undefined && look.br !== null && look.br < 1) {
    parts.push(Math.round(look.br * 100) + "%");
  }
  if (look.v) {
    parts.push("fill " + look.v);
  }
  if (look.when) {
    parts.push("while " + look.when);
  }
  return parts.join(", ");
};

const highlightRows = function () {
  const rows = $("#sceneGrid tbody tr");
  rows.removeClass("applies current previewing");
  if (!state.data || !state.data.active) {
    return;
  }
  if (state.view === "preview") {
    rows.filter("[data-moment='" + state.previewMoment + "']").addClass("previewing");
    return;
  }
  const active = state.live ? state.live.active || [] : [];
  active.forEach(function (moment, i) {
    rows.filter("[data-moment='" + moment + "']").addClass(i === 0 ? "current" : "applies");
  });
};

// ---------------------------------------------------------------------------------------------------------------
// Problems, the moment, Test, Identify and Preview

const problemText = function (problem) {
  if (problem.moment) {
    const target = targetById(problem.target);
    return (target ? targetName(target) : problem.target) + " at " + momentName(problem.moment) + ": " +
      problem.message;
  }
  if (problem.fixture) {
    const fixture = state.data.config.fixtures.find(function (candidate) { return candidate.id === problem.fixture; });
    return (fixture ? fixture.name : problem.fixture) + ": " + problem.message;
  }
  return problem.message;
};

let lastProblems = "";
const renderProblems = function () {
  const data = state.data;
  const problems = data.problems.slice();
  if (data.active && state.live && state.live.profile === data.profile) {
    (state.live.errors || []).forEach(function (problem) { problems.push(problem); });
  }
  if (data.status.loadError && data.active) {
    problems.push({message: data.status.loadError});
  }
  const signature = JSON.stringify(problems);
  if (signature === lastProblems) {
    return;
  }
  lastProblems = signature;
  const list = $("#problems").empty();
  $("#problemsCard").toggleClass("d-none", problems.length === 0);
  problems.forEach(function (problem) {
    const item = $("<li>").addClass(problem.warning ? "alert-warning" : "alert-danger")
      .append($("<i class='bi me-2'>").addClass(problem.warning ? "bi-exclamation-triangle-fill" : "bi-x-circle-fill"),
        document.createTextNode(problemText(problem)));
    if (problem.moment && targetById(problem.target) && momentById(problem.moment)) {
      item.addClass("clickable").on("click", function () { openCellEditor(problem.moment, problem.target); });
    }
    list.append(item);
  });
};

const updateMoment = function () {
  if (!state.data) {
    return;
  }
  const badge = $("#momentBadge").removeClass("testing previewing");
  const live = state.live;
  const testing = live && live.test && live.profile === state.data.profile;
  let text;
  if (!state.data.active) {
    text = "Editing " + state.data.profile;
  } else if (testing) {
    badge.addClass("testing");
    text = "Testing " + momentName(live.test.moment);
  } else if (state.view === "preview") {
    badge.addClass("previewing");
    text = "Preview: " + momentName(state.previewMoment);
  } else {
    text = live && live.moment ? "Now: " + momentName(live.moment) : "Waiting for the field";
  }
  $("#momentText").text(text);
  $("#testButton").toggleClass("d-none", !!testing);
  $("#cancelTest").toggleClass("d-none", !testing);
  updateCountdown();
  highlightRows();
};

const updateCountdown = function () {
  const live = state.live;
  if (live && live.test) {
    $("#testCountdown").text(Math.max(0, Math.ceil((live.test.until - fieldTime()) / 1000)) + " s");
  }
};

const startTest = function (moment) {
  postJson("/setup/lighting/test", {moment: moment}, function (error) {
    if (error) {
      showToast(error.error, "danger");
    } else {
      $("#testMoment").val(moment);
      showToast("Testing " + momentName(moment) + " on the field for 10 seconds.", "warning");
    }
  });
};

const identifyFixture = function (fixture) {
  postJson("/setup/lighting/fixtures/" + encodeURIComponent(fixture.id) + "/identify", undefined, function (error) {
    if (error) {
      showToast(fixture.name + ": " + error.error + ". The fixture still blinks if its channel is driven from here.",
        "warning");
    } else {
      showToast(fixture.name + " is identifying itself.", "success");
    }
  });
};

const setView = function (view) {
  state.view = view;
  $("#viewLive").prop("checked", view === "live");
  $("#viewPreview").prop("checked", view === "preview");
  $("#previewMoment").toggleClass("d-none", view !== "preview");
  sendPreview();
  updateMoment();
  repaintAll();
};

// sendPreview tells the feed which moment to preview, if any. A socket that is not open yet is told when it opens.
const sendPreview = function () {
  try {
    websocket.send("preview", state.view === "preview" ? state.previewMoment : "");
  } catch (error) {
    console.log("The preview will be asked for when the connection opens: " + error);
  }
};

const previewMoment = function (moment) {
  if (!state.data.active) {
    return;
  }
  state.previewMoment = moment;
  $("#previewMoment").val(moment);
  setView("preview");
};

// ---------------------------------------------------------------------------------------------------------------
// The fixture editor

const fixtureEditor = {fixture: null, create: false, idEdited: false, nameEdited: false, segments: [], drag: null};

const slug = function (text) {
  return String(text).toLowerCase().replace(/[^a-z0-9_-]+/g, "-").replace(/^-+|-+$/g, "").slice(0, 40);
};

const openFixtureEditor = function (fixture) {
  const editor = fixtureEditor;
  editor.create = !fixture;
  editor.fixture = fixture ? JSON.parse(JSON.stringify(fixture)) :
    {id: "", name: "", device: "", channel: "", kind: "", pixels: 0, alliance: "", reverse: false, segments: []};
  editor.segments = editor.fixture.segments || [];
  editor.idEdited = !!fixture;
  editor.nameEdited = !!fixture;
  $("#fixtureTitle").text(fixture ? "Edit " + fixture.name : "Add fixture");

  const select = $("#fixtureChannel").empty();
  if (editor.create) {
    select.append($("<option value=''>").text("Choose an output…"));
  }
  const used = {};
  state.data.config.fixtures.forEach(function (other) {
    if (!fixture || other.id !== fixture.id) {
      used[other.device + "/" + other.channel] = other.name;
    }
  });
  let found = false;
  state.data.devices.forEach(function (device) {
    let label = device.name + (device.name !== device.id ? " (" + device.id + ")" : "");
    label += device.online ? "" : " · offline";
    label += device.state === "approved" ? "" : " · " + device.state;
    const group = $("<optgroup>").attr("label", label);
    device.channels.forEach(function (channel) {
      const value = device.id + "/" + channel.id;
      let text = channel.name + " · " + (KINDS[channel.type] || {name: channel.type}).name +
        (channel.pixels ? ", " + channel.pixels + " LEDs" : "");
      const option = $("<option>").val(value).data({channel: channel, device: device});
      const current = fixture && value === fixture.device + "/" + fixture.channel;
      if (used[value]) {
        text += " · shown by " + used[value];
        option.prop("disabled", true);
      } else if (channel.drivenBy && state.data.active && !current) {
        text += " · driven by " + channel.drivenBy;
        option.prop("disabled", true);
      }
      found = found || current;
      group.append(option.text(text));
    });
    select.append(group);
  });
  if (fixture && !found) {
    select.append($("<option>").val(fixture.device + "/" + fixture.channel)
      .text(fixture.device + " · " + fixture.channel + " (not connected)")
      .data({channel: {id: fixture.channel, name: fixture.channel, type: fixture.kind, pixels: fixture.pixels}}));
  }
  select.val(fixture ? fixture.device + "/" + fixture.channel : "");

  $("#fixtureId").val(editor.fixture.id).prop("disabled", !!fixture);
  $("#fixtureName").val(editor.fixture.name);
  const alliance = $("#fixtureAlliance").empty().append($("<option value=''>").text("None"));
  state.data.game.alliances.forEach(function (option) {
    alliance.append($("<option>").val(option.id).text(option.name));
  });
  alliance.val(editor.fixture.alliance || "");
  $("#fixtureBrightness").val(Math.round(brightnessOf(editor.fixture) * 100));
  $("#fixtureReverse").prop("checked", !!editor.fixture.reverse);
  $("#deleteFixture").toggleClass("d-none", !fixture);
  $("#fixtureErrors").empty();
  updateFixtureForm();
  bootstrap.Modal.getOrCreateInstance(document.getElementById("fixtureModal")).show();
};

const updateFixtureForm = function () {
  const fixture = fixtureEditor.fixture;
  const strip = fixture.kind === "led_strip";
  $("#fixtureModal .strip-only").toggleClass("d-none", !strip);
  $("#fixtureModal .not-relay").toggleClass("d-none", !strip && fixture.kind !== "pwm");
  $("#fixtureBrightnessValue").text($("#fixtureBrightness").val() + "%");
  const option = $("#fixtureChannel option:selected");
  const device = option.data("device");
  $("#fixtureChannelHelp").text(!fixture.kind ? "The devices' LED strips, lights, relays and dimmers." :
    (KINDS[fixture.kind] || {name: fixture.kind}).name + (strip ? ", " + fixture.pixels + " LEDs" : "") +
    (device ? (device.online ? " · online" : " · offline") : " · not connected"));
  renderSegments();
};

const onChannelChosen = function () {
  const editor = fixtureEditor;
  const option = $("#fixtureChannel option:selected");
  const value = option.val() || "";
  const channel = option.data("channel");
  const device = option.data("device");
  if (!value || !channel) {
    editor.fixture.device = editor.fixture.channel = editor.fixture.kind = "";
    updateFixtureForm();
    return;
  }
  [editor.fixture.device, editor.fixture.channel] = value.split("/");
  editor.fixture.kind = channel.type;
  editor.fixture.pixels = channel.pixels || 0;
  if (!editor.idEdited) {
    $("#fixtureId").val(slug(editor.fixture.device + "-" + editor.fixture.channel));
  }
  if (!editor.nameEdited && device) {
    const single = device.channels.length === 1;
    $("#fixtureName").val(single ? device.name : device.name + " " + channel.name);
  }
  editor.segments = editor.segments.filter(function (segment) {
    return segment.start + segment.count <= editor.fixture.pixels;
  });
  updateFixtureForm();
};

const segmentColor = function (segment, index) {
  return allianceColor(segment.alliance) || SEGMENT_COLORS[index % SEGMENT_COLORS.length];
};

// invalidSegments returns the indices of segments that overlap another or run off the strip.
const invalidSegments = function () {
  const pixels = fixtureEditor.fixture.pixels;
  const segments = fixtureEditor.segments;
  const invalid = new Set();
  segments.forEach(function (segment, i) {
    if (!(segment.count >= 1) || segment.start < 0 || segment.start + segment.count > pixels) {
      invalid.add(i);
    }
    segments.forEach(function (other, j) {
      if (i !== j && segment.start < other.start + other.count && other.start < segment.start + segment.count) {
        invalid.add(i);
      }
    });
  });
  return invalid;
};

const renderSegments = function () {
  renderSegmentBlocks();
  renderSegmentRows();
};

const renderSegmentBlocks = function () {
  const pixels = fixtureEditor.fixture.pixels;
  const strip = $("#segmentStrip");
  strip.find(".segment-block").remove();
  $("#segmentPixels").text(pixels ? "(" + pixels + " LEDs)" : "");
  $("#segmentScaleEnd").text(pixels ? pixels - 1 : "");
  if (!pixels) {
    return;
  }
  const invalid = invalidSegments();
  fixtureEditor.segments.forEach(function (segment, index) {
    strip.append($("<div class='segment-block'>").attr("data-index", index)
      .toggleClass("invalid", invalid.has(index))
      .css({
        left: (segment.start / pixels * 100) + "%", width: (Math.max(0, segment.count) / pixels * 100) + "%",
        "background-color": segmentColor(segment, index),
      })
      .append($("<span>").text(segment.name || segment.id), $("<span class='handle start'>"),
        $("<span class='handle end'>")));
  });
  const canvas = strip.find(".led-marks")[0];
  const context = fitCanvas(canvas);
  context.clearRect(0, 0, canvas.width, canvas.height);
  const step = canvas.width / pixels;
  if (step >= 3) {
    context.fillStyle = "#9aa4af";
    for (let i = 0; i < pixels; i++) {
      context.fillRect(Math.round(i * step + step / 2) - 1, canvas.height / 2 - 2, 2, 4);
    }
  }
};

const renderSegmentRows = function () {
  const rows = $("#segmentRows").empty();
  fixtureEditor.segments.forEach(function (segment, index) {
    const update = function (field, transform) {
      return function () {
        segment[field] = transform ? transform(this.value) : this.value;
        renderSegmentBlocks();
      };
    };
    const alliance = $("<select class='form-select form-select-sm'>").append($("<option value=''>").text("Fixture's"));
    state.data.game.alliances.forEach(function (option) {
      alliance.append($("<option>").val(option.id).text(option.name));
    });
    alliance.val(segment.alliance || "").on("change", update("alliance"));
    const number = function (field) {
      return $("<input type='number' class='form-control form-control-sm' min='0'>").val(segment[field])
        .attr("data-field", field).on("input", update(field, function (value) { return parseInt(value, 10) || 0; }));
    };
    rows.append($("<tr>").attr("data-index", index).append(
      $("<td>").append($("<input class='form-control form-control-sm font-monospace' maxlength='40'>")
        .val(segment.id).on("input", update("id"))),
      $("<td>").append($("<input class='form-control form-control-sm'>").val(segment.name)
        .attr("placeholder", segment.id).on("input", update("name"))),
      $("<td>").append(number("start")),
      $("<td>").append(number("count")),
      $("<td>").append(alliance),
      $("<td>").append($("<button type='button' class='btn btn-outline-danger btn-sm'>")
        .attr("title", "Remove the segment").append($("<i class='bi bi-trash'>")).on("click", function () {
          fixtureEditor.segments.splice(index, 1);
          renderSegments();
        })),
    ));
  });
};

const updateSegmentRow = function (index) {
  const segment = fixtureEditor.segments[index];
  const row = $("#segmentRows tr[data-index='" + index + "']");
  row.find("[data-field='start']").val(segment.start);
  row.find("[data-field='count']").val(segment.count);
};

const nextSegmentId = function () {
  for (let i = 1; ; i++) {
    const id = "seg" + i;
    if (!fixtureEditor.segments.some(function (segment) { return segment.id === id; })) {
      return id;
    }
  }
};

// freeRange returns the LEDs [low, high) a segment may take without running into the others, around a pixel.
const freeRange = function (index, pixel) {
  let low = 0;
  let high = fixtureEditor.fixture.pixels;
  fixtureEditor.segments.forEach(function (other, i) {
    if (i === index) {
      return;
    }
    if (other.start + other.count <= pixel) {
      low = Math.max(low, other.start + other.count);
    } else if (other.start > pixel) {
      high = Math.min(high, other.start);
    }
  });
  return [low, high];
};

const pixelAt = function (event) {
  const rect = document.getElementById("segmentStrip").getBoundingClientRect();
  const pixels = fixtureEditor.fixture.pixels;
  return Math.max(0, Math.min(pixels - 1, Math.floor((event.clientX - rect.left) / rect.width * pixels)));
};

const setupSegmentStrip = function () {
  const strip = document.getElementById("segmentStrip");
  strip.addEventListener("pointerdown", function (event) {
    const editor = fixtureEditor;
    if (!editor.fixture || !editor.fixture.pixels) {
      return;
    }
    const pixel = pixelAt(event);
    const block = event.target.closest(".segment-block");
    if (block) {
      const index = parseInt(block.dataset.index, 10);
      const segment = editor.segments[index];
      if (event.target.classList.contains("handle")) {
        editor.drag = {mode: event.target.classList.contains("start") ? "start" : "end", index: index};
      } else {
        editor.drag = {mode: "move", index: index, offset: pixel - segment.start};
      }
    } else {
      const [low, high] = freeRange(-1, pixel);
      if (pixel < low || pixel >= high) {
        return;
      }
      editor.segments.push({id: nextSegmentId(), name: "", start: pixel, count: 1, alliance: ""});
      editor.drag = {mode: "create", index: editor.segments.length - 1, anchor: pixel};
      renderSegments();
    }
    strip.setPointerCapture(event.pointerId);
    event.preventDefault();
  });
  strip.addEventListener("pointermove", function (event) {
    const editor = fixtureEditor;
    const drag = editor.drag;
    if (!drag) {
      return;
    }
    const pixel = pixelAt(event);
    const segment = editor.segments[drag.index];
    const end = segment.start + segment.count;
    const [low, high] = freeRange(drag.index, drag.mode === "create" ? drag.anchor : segment.start);
    switch (drag.mode) {
      case "create": {
        const from = Math.max(low, Math.min(drag.anchor, pixel));
        const to = Math.min(high - 1, Math.max(drag.anchor, pixel));
        segment.start = from;
        segment.count = to - from + 1;
        break;
      }
      case "move":
        segment.start = Math.max(low, Math.min(high - segment.count, pixel - drag.offset));
        break;
      case "start":
        segment.start = Math.max(low, Math.min(end - 1, pixel));
        segment.count = end - segment.start;
        break;
      case "end":
        segment.count = Math.max(1, Math.min(high, pixel + 1) - segment.start);
        break;
    }
    renderSegmentBlocks();
    updateSegmentRow(drag.index);
  });
  const stop = function () {
    fixtureEditor.drag = null;
  };
  strip.addEventListener("pointerup", stop);
  strip.addEventListener("pointercancel", stop);
};

const showProblems = function (container, response, filter) {
  container.empty();
  if (!response) {
    return;
  }
  const problems = (response.problems || []).filter(filter || function () { return true; });
  const errors = problems.filter(function (problem) { return !problem.warning; });
  if (errors.length === 0 && response.error) {
    container.append($("<div class='alert alert-danger py-2 mb-2'>").text(response.error));
  }
  problems.forEach(function (problem) {
    container.append($("<div class='alert py-2 mb-2'>").addClass(problem.warning ? "alert-warning" : "alert-danger")
      .text(problem.message.charAt(0).toUpperCase() + problem.message.slice(1) + "."));
  });
};

const saveFixture = function () {
  const editor = fixtureEditor;
  if (!editor.fixture.device) {
    showProblems($("#fixtureErrors"), {error: "Choose the output this fixture is."});
    return;
  }
  const fixture = {
    id: $("#fixtureId").val().trim(), name: $("#fixtureName").val().trim(), device: editor.fixture.device,
    channel: editor.fixture.channel, kind: editor.fixture.kind, pixels: editor.fixture.pixels,
    alliance: $("#fixtureAlliance").val(), reverse: $("#fixtureReverse").prop("checked"),
    brightness: parseInt($("#fixtureBrightness").val(), 10) / 100,
    segments: editor.fixture.kind === "led_strip" ? editor.segments.map(function (segment) {
      return {
        id: String(segment.id).trim(), name: String(segment.name || "").trim(), start: segment.start,
        count: segment.count, alliance: segment.alliance || "",
      };
    }) : [],
  };
  postJson("/setup/lighting/fixtures" + profileQuery(), {fixture: fixture, create: editor.create},
    function (error, response) {
      if (error) {
        showProblems($("#fixtureErrors"), error, function (problem) {
          return problem.fixture === fixture.id && !problem.moment;
        });
        return;
      }
      bootstrap.Modal.getInstance(document.getElementById("fixtureModal")).hide();
      const warnings = response.problems.filter(function (problem) {
        return problem.warning && problem.fixture === fixture.id && !problem.moment;
      });
      showToast(fixture.name + " saved." + (warnings.length ? " " + warnings[0].message + "." : ""),
        warnings.length ? "warning" : "success");
      loadData();
    });
};

const deleteFixture = function () {
  const fixture = fixtureEditor.fixture;
  if (!confirm("Delete " + fixture.name + " and its scenes in every game?")) {
    return;
  }
  postJson("/setup/lighting/fixtures/" + encodeURIComponent(fixture.id) + "/delete" + profileQuery(), undefined,
    function (error) {
      if (error) {
        showProblems($("#fixtureErrors"), error);
        return;
      }
      bootstrap.Modal.getInstance(document.getElementById("fixtureModal")).hide();
      loadData();
    });
};

// ---------------------------------------------------------------------------------------------------------------
// The cell editor

const cellEditor = {moment: null, target: null, pattern: "solid", periodDefault: true, colorA: null, colorB: null};

// makeColorChooser builds a colour control: Alliance, Winner, Off or a custom colour, and None for a second colour.
const makeColorChooser = function (container, allowNone, onChange) {
  const chooser = {value: allowNone ? "" : WHITE, label: $("<div class='form-label'>")};
  const name = "choice-" + container.attr("id");
  const group = $("<div class='btn-group btn-group-sm' role='group'>");
  const options = (allowNone ? [["", "None"]] : [])
    .concat([["alliance", "Alliance"], ["winner", "Winner"], ["off", "Off"], ["custom", "Custom"]]);
  options.forEach(function (option, i) {
    const id = name + "-" + i;
    group.append($("<input type='radio' class='btn-check' autocomplete='off'>").attr({name: name, id: id})
      .val(option[0]), $("<label class='btn btn-outline-secondary'>").attr("for", id).text(option[1]));
  });
  const picker = $("<input type='color' class='form-control form-control-color' title='Custom colour'>")
    .val(WHITE);
  const palette = $("<div class='palette'>");
  PALETTE.forEach(function (color) {
    palette.append($("<button type='button'>").css("background", color).attr({"data-color": color, title: color}));
  });
  container.empty().append(chooser.label,
    $("<div class='d-flex align-items-center gap-2 flex-wrap'>").append(group, picker), palette);
  const sync = function () {
    const custom = /^#/.test(chooser.value);
    group.find("input").each(function () {
      this.checked = custom ? this.value === "custom" : this.value === chooser.value;
    });
    if (custom) {
      picker.val(chooser.value);
    }
    palette.find("button").each(function () {
      $(this).toggleClass("selected", custom && this.dataset.color === chooser.value);
    });
  };
  group.on("change", "input", function () {
    chooser.value = this.value === "custom" ? picker.val().toLowerCase() : this.value;
    sync();
    onChange();
  });
  picker.on("input", function () {
    chooser.value = this.value.toLowerCase();
    sync();
    onChange();
  });
  palette.on("click", "button", function () {
    chooser.value = this.dataset.color;
    sync();
    onChange();
  });
  chooser.set = function (value) {
    chooser.value = value === undefined || value === null ? (allowNone ? "" : WHITE) : value;
    sync();
  };
  return chooser;
};

const openCellEditor = function (momentId, targetId) {
  const moment = momentById(momentId);
  const target = targetById(targetId);
  if (!moment || !target) {
    return;
  }
  const existing = cellLook(momentId, targetId);
  const index = state.data.moments.indexOf(moment);
  const inherited = inheritedLook(index, target);
  const look = JSON.parse(JSON.stringify(existing || (inherited ? inherited.look : {p: "solid", c: [WHITE]})));
  cellEditor.moment = momentId;
  cellEditor.target = target;
  cellEditor.pattern = look.p || "solid";
  cellEditor.periodDefault = !look.ms;

  $("#cellTitle").text(moment.name + " · " + targetName(target));
  $("#cellSubtitle").text(moment.when + "." + (existing ? "" : inherited ?
    " Empty now: it falls through to " + inherited.moment.name + ", whose look this starts from." :
    " Empty now."));
  const kind = target.fixture.kind;
  const allowed = kind === "led_strip" ? STRIP_PATTERNS : LEVEL_PATTERNS[kind] || STRIP_PATTERNS;
  if (allowed.indexOf(cellEditor.pattern) < 0) {
    cellEditor.pattern = "solid";
  }
  const buttons = $("#cellPatterns").empty();
  allowed.forEach(function (pattern) {
    buttons.append($("<button type='button' class='btn btn-outline-light'>").attr("data-pattern", pattern)
      .append($("<i class='bi me-1'>").addClass(PATTERNS[pattern].icon), PATTERNS[pattern].name)
      .on("click", function () {
        cellEditor.pattern = pattern;
        updateCellForm();
      }));
  });
  const colors = look.c || [];
  cellEditor.colorA.set(colors[0] || WHITE);
  cellEditor.colorB.set(colors[1] || "");
  const period = look.ms || LedPatterns.defaultPeriods[cellEditor.pattern] || LEVEL_BLINK_MS;
  $("#cellPeriod").val(period);
  $("#cellBrightness").val(Math.round((look.br === undefined || look.br === null ? 1 : look.br) * 100));
  $("#cellV").val(look.v || "");
  $("#cellWhen").val(look.when || "");
  $("#clearCell").toggleClass("d-none", !existing);
  $("#cellErrors").empty();
  updateCellForm();
  bootstrap.Modal.getOrCreateInstance(document.getElementById("cellModal")).show();
};

const updateCellForm = function () {
  const pattern = cellEditor.pattern;
  const strip = cellEditor.target.fixture.kind === "led_strip";
  $("#cellPatterns button").each(function () {
    $(this).toggleClass("active", this.dataset.pattern === pattern);
  });
  const twoColors = ["blink", "pulse", "chase", "progress"].indexOf(pattern) >= 0;
  $("#cellColors").toggleClass("d-none", !strip || pattern === "off" || pattern === "rainbow");
  $("#colorB").toggleClass("d-none", !twoColors);
  cellEditor.colorA.label.text({progress: "Filled part", chase: "Moving light", pulse: "Bright colour"}[pattern] ||
    "Colour");
  cellEditor.colorB.label.text({progress: "Empty part", chase: "Background", pulse: "Dim colour"}[pattern] ||
    "Alternates with");
  const periodic = ["blink", "pulse", "chase", "rainbow"].indexOf(pattern) >= 0;
  $("#cellPeriodGroup").toggleClass("d-none", !periodic);
  const defaultPeriod = strip ? LedPatterns.defaultPeriods[pattern] : LEVEL_BLINK_MS;
  if (cellEditor.periodDefault && defaultPeriod) {
    $("#cellPeriod").val(defaultPeriod);
  }
  $("#cellPeriodValue").text($("#cellPeriod").val() + " ms" + (cellEditor.periodDefault ? " (default)" : ""));
  $("#cellPeriodDefault").prop("disabled", cellEditor.periodDefault);
  $("#cellBrightnessGroup").toggleClass("d-none", pattern === "off" ||
    (!strip && cellEditor.target.fixture.kind !== "pwm"));
  $("#cellBrightnessValue").text($("#cellBrightness").val() + "%");
  $("#cellFillGroup").toggleClass("d-none", pattern !== "progress");
  $("#cellPreview").toggleClass("lamp", !strip);
};

const readLook = function () {
  const pattern = cellEditor.pattern;
  const look = {p: pattern};
  const strip = cellEditor.target.fixture.kind === "led_strip";
  if (strip && pattern !== "off" && pattern !== "rainbow") {
    look.c = [cellEditor.colorA.value];
    if (["blink", "pulse", "chase", "progress"].indexOf(pattern) >= 0 && cellEditor.colorB.value) {
      look.c.push(cellEditor.colorB.value);
    }
  }
  if (["blink", "pulse", "chase", "rainbow"].indexOf(pattern) >= 0 && !cellEditor.periodDefault) {
    look.ms = parseInt($("#cellPeriod").val(), 10);
  }
  const brightness = parseInt($("#cellBrightness").val(), 10);
  if (!$("#cellBrightnessGroup").hasClass("d-none") && brightness < 100) {
    look.br = brightness / 100;
  }
  if (pattern === "progress") {
    look.v = $("#cellV").val().trim();
  }
  const when = $("#cellWhen").val().trim();
  if (when) {
    look.when = when;
  }
  return look;
};

const saveCell = function (look) {
  const request = {moment: cellEditor.moment, target: cellEditor.target.id, look: look};
  postJson("/setup/lighting/cell" + profileQuery(), request, function (error, response) {
    if (error) {
      showProblems($("#cellErrors"), error);
      return;
    }
    bootstrap.Modal.getInstance(document.getElementById("cellModal")).hide();
    const warnings = response.problems.filter(function (problem) { return problem.warning; });
    if (warnings.length > 0) {
      showToast("Saved. " + warnings[0].message + ".", "warning");
    }
    loadData();
  });
};

const setupCellEditor = function () {
  const changed = function () {
    updateCellForm();
  };
  cellEditor.colorA = makeColorChooser($("#colorA"), false, changed);
  cellEditor.colorB = makeColorChooser($("#colorB"), true, changed);
  $("#cellPeriod").on("input", function () {
    cellEditor.periodDefault = false;
    updateCellForm();
  });
  $("#cellPeriodDefault").on("click", function () {
    cellEditor.periodDefault = true;
    updateCellForm();
  });
  $("#cellBrightness").on("input", updateCellForm);
  FILL_PRESETS.forEach(function (preset) {
    $("#cellFillPresets").append($("<button type='button' class='btn btn-outline-secondary btn-sm'>").text(preset[0])
      .attr("title", preset[1]).on("click", function () { $("#cellV").val(preset[1]); }));
  });
  $("#saveCell").on("click", function () { saveCell(readLook()); });
  $("#clearCell").on("click", function () { saveCell(null); });
  const preview = document.getElementById("cellPreview");
  addPainter(preview, function (t) {
    if (cellEditor.target && $("#cellModal").hasClass("show")) {
      paintLook(preview, cellEditor.target, readLook(), cellEditor.moment, EDITOR_PIXELS, t, 1);
    }
  }, function () { return $("#cellModal").hasClass("show"); });
};

// ---------------------------------------------------------------------------------------------------------------
// Import and export

const importFile = function (file) {
  const reader = new FileReader();
  reader.onload = function () {
    $.ajax({
      url: "/setup/lighting/import" + profileQuery(), type: "POST", contentType: "application/yaml",
      data: reader.result, dataType: "json", processData: false,
    }).done(function (response) {
      const warnings = (response.problems || []).filter(function (problem) { return problem.warning; });
      showImportResult("success", file.name + " imported into " + state.data.profile + ".", warnings);
      loadData();
    }).fail(function (xhr) {
      const response = xhr.responseJSON || {error: xhr.statusText, problems: []};
      showImportResult("danger", file.name + " was not imported: " + response.error, []);
    });
  };
  reader.readAsText(file);
};

const showImportResult = function (kind, message, warnings) {
  const result = $("#importResult").removeClass("d-none").empty();
  const alert = $("<div class='alert alert-dismissible'>").addClass("alert-" + kind)
    .append($("<div>").text(message),
      $("<button type='button' class='btn-close' aria-label='Close'>").on("click", function () {
        result.addClass("d-none");
      }));
  warnings.forEach(function (problem) {
    alert.append($("<div class='small'>").text(problemText(problem)));
  });
  result.append(alert);
};

// ---------------------------------------------------------------------------------------------------------------
// Start

$(function () {
  setupSegmentStrip();
  setupCellEditor();

  $("#profileSelect").on("change", function () {
    state.profile = this.value;
    loadData();
  });
  $("input[name='viewMode']").on("change", function () { setView(this.value); });
  $("#previewMoment").on("change", function () {
    state.previewMoment = this.value;
    setView("preview");
  });
  $("#testButton").on("click", function () { startTest($("#testMoment").val()); });
  $("#cancelTest").on("click", function () {
    postJson("/setup/lighting/test/cancel", undefined, function (error) {
      if (error) {
        showToast(error.error, "danger");
      }
    });
  });
  $("#addFixture").on("click", function () { openFixtureEditor(null); });
  $("#fixtureChannel").on("change", onChannelChosen);
  $("#fixtureId").on("input", function () { fixtureEditor.idEdited = true; });
  $("#fixtureName").on("input", function () { fixtureEditor.nameEdited = true; });
  $("#fixtureBrightness").on("input", function () {
    $("#fixtureBrightnessValue").text(this.value + "%");
  });
  $("#addSegment").on("click", function () {
    const editor = fixtureEditor;
    const pixels = editor.fixture.pixels;
    const end = editor.segments.reduce(function (last, segment) {
      return Math.max(last, segment.start + segment.count);
    }, 0);
    if (!pixels || end >= pixels) {
      showProblems($("#fixtureErrors"), {error: "There is no room left on the strip for another segment."});
      return;
    }
    editor.segments.push({
      id: nextSegmentId(), name: "", start: end, count: Math.max(1, Math.min(pixels - end, Math.ceil(pixels / 4))),
      alliance: "",
    });
    renderSegments();
  });
  $("#fixtureModal").on("shown.bs.modal", renderSegmentBlocks);
  $("#saveFixture").on("click", saveFixture);
  $("#deleteFixture").on("click", deleteFixture);
  $("#importButton").on("click", function () { $("#importFile").val("").trigger("click"); });
  $("#importFile").on("change", function () {
    if (this.files.length > 0) {
      importFile(this.files[0]);
    }
  });
  $(window).on("resize", repaintAll);

  websocket = new CheesyWebsocket("/setup/lighting/websocket", {
    lightingLive: function (event) {
      state.live = event.data;
      syncClock(event.data.time);
      if (state.data) {
        updateMoment();
        renderProblems();
      }
    },
    lightingConfig: function (event) {
      if (state.data && state.data.active && event.data.revision !== state.revision) {
        loadData();
      }
    },
    lightingCells: function (event) {
      state.cells = event.data || {};
      if (state.data) {
        $("#sceneGrid .cell-flag").each(function () {
          const cell = $(this).closest(".scene-cell");
          const live = (state.cells[cell.attr("data-moment")] || {})[cell.attr("data-target")];
          $(this).toggleClass("off", !!live && !live.when);
        });
      }
    },
    lightingPreview: function (event) {
      state.preview = event.data;
    },
    lightingDevices: function (event) {
      if (state.data) {
        state.data.devices = event.data || [];
        renderStrips();
        renderFixtures();
      }
    },
    lightingClock: function (event) {
      syncClock(event.data.time);
    },
  }, {
    open: function () {
      if (state.view === "preview") {
        sendPreview();
      }
    },
  });

  loadData();
  setInterval(updateCountdown, 250);
  requestAnimationFrame(frame);
});
