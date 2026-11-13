// Client-side logic for the Bindings page: the binding set of the active game and field profile, edited as cards
// that read as sentences ("When beam.count counts, add the count to <element> for <alliance>"), or as YAML, and
// checked with the FMS on every change. Every element, phase, alliance and station offered comes from the game and
// the devices.

// What each score operation can change, by element type, and which inputs can drive it (docs/devices.md).
const ELEMENT_OPS = {
  counter: ["increment", "decrement", "set"],
  number: ["increment", "decrement", "set"],
  boolean: ["set", "toggle"],
  enum: ["set"],
  timer: ["set", "hold", "timer_start", "timer_stop"],
};
const INPUT_OPS = {
  increment: ["digital", "counter"],
  decrement: ["digital", "counter"],
  set: ["digital", "analog", "encoder", "text"],
  toggle: ["digital"],
  hold: ["digital"],
  timer_start: ["digital"],
  timer_stop: ["digital"],
};
const SCORE_ACTIONS = [
  ["increment", "add"], ["decrement", "subtract"], ["set", "set"], ["toggle", "toggle"],
  ["hold", "run the timer"], ["timer_start", "start the timer"], ["timer_stop", "stop the timer"],
];
// The word between a score action and its element.
const ACTION_LINKS = {increment: "to", decrement: "from", hold: "of", timer_start: "of", timer_stop: "of"};
const FIELD_ACTIONS = [
  ["start_match", "start the match"], ["abort_match", "abort the match"], ["estop", "E-stop station"],
  ["astop", "A-stop station"], ["field_stop", "stop the whole field"], ["field_reset", "signal field reset"],
  ["volunteers", "call the volunteers"], ["commit", "commit and post the score"], ["next_match", "load the next match"],
];
const STOP_ACTIONS = ["estop", "astop", "field_stop"];
const PATTERNS = ["solid", "blink", "pulse", "chase", "rainbow", "progress", "off"];
const TIMED_PATTERNS = ["blink", "pulse", "chase", "rainbow"];

const BindingsPage = {
  set: {inputs: [], outputs: []},  // The set being edited, as in the YAML, plus _key and _autoId.
  savedJson: "",                   // The set as last loaded or saved.
  problems: [],
  stations: [],
  game: "",
  profile: "",
  lastYaml: "",
  yamlMode: false,
  validateTimer: null,
  validateNumber: 0,
  nextKey: 1,
  optionsSignature: "",
  renderPending: false,

  start: function () {
    GameView.onChange(function () {
      BindingsPage.renderCards();
    });
    DevicePages.onChange(BindingsPage.devicesChanged);
    DevicePages.onEvent(BindingsPage.showLive);
    DevicePages.connect({game: function (event) { handleGame(event.data); }});
    BindingsPage.load();
    setInterval(function () {
      BindingsPage.updateLive();
      if (BindingsPage.renderPending && $(".binding-card :focus").length === 0) {
        BindingsPage.renderCards();
      }
    }, 1000);

    $("#addInput").on("click", BindingsPage.addInput);
    $("#addOutput").on("click", BindingsPage.addOutput);
    $("#saveBindings").on("click", BindingsPage.save);
    $("#revertBindings").on("click", function () {
      if (confirm("Discard the changes since the bindings were last saved?")) {
        BindingsPage.load();
      }
    });
    $("#importBindings").on("click", function (event) {
      event.preventDefault();
      $("#importFile").val("").trigger("click");
    });
    $("#importFile").on("change", BindingsPage.importFile);
    $("[data-tab]").on("click", function (event) {
      event.preventDefault();
      BindingsPage.switchTab($(this).attr("data-tab"));
    });
    $("#yamlEditor").on("input", function () {
      BindingsPage.scheduleValidation();
      BindingsPage.updateStatus();
    });
    $(document).on("change", ".binding-card [data-field]", BindingsPage.fieldChanged);
    $(document).on("input", ".binding-card [data-text]", BindingsPage.textChanged);
    $(document).on("click", ".binding-card [data-button]", BindingsPage.buttonClicked);
    $(document).on("focusout", ".binding-card", function () {
      setTimeout(function () {
        if (BindingsPage.renderPending && $(".binding-card :focus").length === 0) {
          BindingsPage.renderCards();
        }
      }, 0);
    });
    window.addEventListener("beforeunload", function (event) {
      if (BindingsPage.isDirty()) {
        event.preventDefault();
        event.returnValue = "";
      }
    });
  },

  // ---------------------------------------------------------------------------------------------------------------
  // Loading, checking and saving

  load: function () {
    DevicePages.request("GET", "/setup/bindings/state").then(function (data) {
      BindingsPage.setFromServer(data, true);
      BindingsPage.scrollToHash();
    }).catch(DevicePages.fail);
  },

  setFromServer: function (data, saved) {
    BindingsPage.game = data.game;
    BindingsPage.profile = data.profile;
    BindingsPage.stations = data.stations || BindingsPage.stations;
    BindingsPage.set = BindingsPage.withKeys(data.set || {});
    BindingsPage.problems = data.problems || [];
    BindingsPage.lastYaml = data.yaml || "";
    if (saved) {
      BindingsPage.savedJson = BindingsPage.json();
    }
    if (BindingsPage.yamlMode) {
      $("#yamlEditor").val(BindingsPage.lastYaml);
    }
    $("#profileName").text(data.profile);
    BindingsPage.renderCards();
  },

  withKeys: function (set) {
    const keyed = {inputs: set.inputs || [], outputs: set.outputs || []};
    keyed.inputs.concat(keyed.outputs).forEach(function (binding) {
      binding._key = BindingsPage.nextKey++;
      binding.action = binding.action || (keyed.inputs.includes(binding) ? {} : undefined);
    });
    return keyed;
  },

  // The set as the FMS reads it: without the page's own keys, and without empty fields.
  clean: function (value) {
    if (Array.isArray(value)) {
      return value.map(BindingsPage.clean);
    }
    if (value !== null && typeof value === "object") {
      const result = {};
      Object.keys(value).forEach(function (key) {
        const item = value[key];
        if (key.startsWith("_") || item === undefined || item === null || item === "" ||
          (Array.isArray(item) && item.length === 0)) {
          return;
        }
        result[key] = BindingsPage.clean(item);
      });
      return result;
    }
    return value;
  },

  json: function () {
    return JSON.stringify(BindingsPage.clean(BindingsPage.set));
  },

  isDirty: function () {
    if (BindingsPage.yamlMode) {
      return $("#yamlEditor").val() !== BindingsPage.lastYaml || BindingsPage.json() !== BindingsPage.savedJson;
    }
    return BindingsPage.json() !== BindingsPage.savedJson;
  },

  body: function () {
    return BindingsPage.yamlMode ? $("#yamlEditor").val() : BindingsPage.json();
  },

  scheduleValidation: function () {
    clearTimeout(BindingsPage.validateTimer);
    BindingsPage.validateTimer = setTimeout(BindingsPage.validate, 300);
  },

  // Checks the set with the FMS and shows its problems. In the YAML tab the cards follow what the YAML says.
  validate: function () {
    clearTimeout(BindingsPage.validateTimer);
    const number = ++BindingsPage.validateNumber;
    const yamlMode = BindingsPage.yamlMode;
    return DevicePages.post("/setup/bindings/validate", BindingsPage.body()).then(function (data) {
      if (number !== BindingsPage.validateNumber) {
        return data;
      }
      BindingsPage.problems = data.problems || [];
      if (yamlMode) {
        BindingsPage.set = BindingsPage.withKeys(data.set || {});
      } else {
        BindingsPage.lastYaml = data.yaml || "";
      }
      BindingsPage.showProblems();
      return data;
    }).catch(function (error) {
      if (number === BindingsPage.validateNumber) {
        BindingsPage.problems = (error.data && error.data.problems) || [{message: error.message}];
        BindingsPage.showProblems();
      }
      throw error;
    });
  },

  changed: function () {
    BindingsPage.updateStatus();
    BindingsPage.scheduleValidation();
  },

  save: function () {
    DevicePages.post("/setup/bindings/save", BindingsPage.body()).then(function (data) {
      BindingsPage.setFromServer(data, true);
      if (BindingsPage.yamlMode) {
        $("#yamlEditor").val(data.yaml);
      }
      BindingsPage.updateStatus();
      DevicePages.notify("Saved. The bindings are in force.", "success");
    }).catch(function (error) {
      if (error.data && error.data.problems) {
        BindingsPage.problems = error.data.problems;
        BindingsPage.showProblems();
      }
      DevicePages.notify("Not saved: " + error.message, "danger");
    });
  },

  importFile: function () {
    const file = this.files[0];
    if (!file) {
      return;
    }
    if (BindingsPage.isDirty() && !confirm("Importing replaces the bindings, including the changes not saved. " +
      "Go on?")) {
      return;
    }
    file.text().then(function (text) {
      return DevicePages.post("/setup/bindings/import", text);
    }).then(function (data) {
      BindingsPage.setFromServer(data, true);
      DevicePages.notify("Imported and saved " + file.name + ".", "success");
    }).catch(function (error) {
      if (error.data && error.data.problems) {
        BindingsPage.problems = error.data.problems;
        BindingsPage.showProblems();
      }
      DevicePages.notify("Not imported: " + error.message, "danger");
    });
  },

  switchTab: function (tab) {
    if ((tab === "yamlTab") === BindingsPage.yamlMode) {
      return;
    }
    if (tab === "yamlTab") {
      BindingsPage.validate().catch(function () {}).then(function () {
        $("#yamlEditor").val(BindingsPage.lastYaml);
        BindingsPage.yamlMode = true;
        BindingsPage.showTab(tab);
      });
      return;
    }
    // Leaving the YAML: the cards take what it says, once it reads.
    BindingsPage.validate().then(function (data) {
      BindingsPage.yamlMode = false;
      BindingsPage.lastYaml = data.yaml || "";
      BindingsPage.showTab(tab);
      BindingsPage.renderCards();
    }).catch(function () {
      DevicePages.notify("Fix the YAML before going back to the cards.", "warning");
    });
  },

  showTab: function (tab) {
    $("[data-tab]").removeClass("active").filter("[data-tab='" + tab + "']").addClass("active");
    $("#cardsTab").toggleClass("d-none", tab !== "cardsTab");
    $("#yamlTab").toggleClass("d-none", tab !== "yamlTab");
    BindingsPage.updateStatus();
  },

  updateStatus: function () {
    const dirty = BindingsPage.isDirty();
    const errors = BindingsPage.problems.filter(function (problem) { return !problem.warning; }).length;
    let text = "Saved";
    let kind = "text-bg-success";
    if (errors > 0) {
      text = errors + (errors === 1 ? " error" : " errors") + (dirty ? ", not saved" : "");
      kind = "text-bg-danger";
    } else if (dirty) {
      text = "Not saved";
      kind = "text-bg-warning";
    }
    $("#saveStatus").removeClass("text-bg-success text-bg-danger text-bg-warning").addClass(kind).text(text);
    $("#saveBindings").prop("disabled", !dirty || errors > 0);
    $("#revertBindings").prop("disabled", !dirty);
  },

  // Shows each problem on the card of the binding it belongs to, and the others above the cards.
  showProblems: function () {
    const byBinding = {};
    const general = [];
    const ids = BindingsPage.set.inputs.concat(BindingsPage.set.outputs).map(function (binding) { return binding.id; });
    BindingsPage.problems.forEach(function (problem) {
      if (problem.binding && ids.includes(problem.binding)) {
        (byBinding[problem.binding] = byBinding[problem.binding] || []).push(problem);
      } else {
        general.push(problem);
      }
    });
    const alert = function (problem) {
      return $("<div class='alert'>").addClass(problem.warning ? "alert-warning" : "alert-danger")
        .text((problem.warning ? "Note: " : "") + problem.message);
    };
    $("#generalProblems").empty().append(general.map(function (problem) {
      return alert({warning: problem.warning, message: (problem.binding ? problem.binding + ": " : "") +
        problem.message});
    }));
    $(".binding-card").each(function () {
      const binding = BindingsPage.bindingOf($(this));
      const problems = binding ? byBinding[binding.id] || [] : [];
      $(this).find(".binding-problems").empty().append(problems.map(alert));
      const hasError = problems.some(function (problem) { return !problem.warning; });
      $(this).toggleClass("has-error", hasError).toggleClass("has-warning", !hasError && problems.length > 0)
        .toggleClass("is-ok", problems.length === 0);
    });
    BindingsPage.updateStatus();
  },

  // The game, profile or devices changed: a set saved for another game or profile is not this one.
  devicesChanged: function (state) {
    if (BindingsPage.game !== "" && (state.game !== BindingsPage.game || state.profile !== BindingsPage.profile)) {
      if (BindingsPage.isDirty()) {
        DevicePages.notify("The active game or field profile changed. These bindings are for the previous one; " +
          "they are reloaded once you save or revert.", "warning");
        BindingsPage.game = state.game;
        BindingsPage.profile = state.profile;
      } else {
        BindingsPage.load();
      }
      return;
    }
    const signature = JSON.stringify(state.devices.map(function (info) {
      return [info.id, info.name, info.state, info.manifest];
    }));
    if (signature !== BindingsPage.optionsSignature) {
      BindingsPage.optionsSignature = signature;
      BindingsPage.renderCards();
      BindingsPage.scheduleValidation();
    } else {
      BindingsPage.updateLive();
    }
  },

  scrollToHash: function () {
    const id = decodeURIComponent(window.location.hash.slice(1));
    if (!id) {
      return;
    }
    const card = $(".binding-card").filter(function () {
      const binding = BindingsPage.bindingOf($(this));
      return binding !== null && binding.id === id;
    });
    if (card.length > 0) {
      card[0].scrollIntoView({block: "center"});
      card.addClass("border-info");
    }
  },

  // ---------------------------------------------------------------------------------------------------------------
  // What the game and devices offer

  elementEditable: function (element) {
    return !element.edit || element.edit.length === 0 || element.edit.includes("device");
  },

  // The kind of input a binding reads: digital (a digital channel, or analog or encoder with a threshold), counter,
  // analog, encoder, text, or "" while the device has not told the FMS its channels.
  inputKind: function (binding, channel) {
    if (!channel) {
      return "";
    }
    if ((channel.type === "analog" || channel.type === "encoder") && typeof binding.threshold === "number") {
      return "digital";
    }
    return channel.type;
  },

  elementsFor: function (op, kind) {
    if (!GameView.isReady()) {
      return [];
    }
    return GameView.view.elements.filter(function (element) {
      if (!BindingsPage.elementEditable(element) || !(ELEMENT_OPS[element.type] || []).includes(op)) {
        return false;
      }
      if (op === "set" && (kind === "analog" || kind === "encoder")) {
        return element.type === "number";
      }
      if (op === "set" && kind === "text") {
        return element.type === "enum";
      }
      return true;
    }).sort(function (a, b) { return a.order - b.order; });
  },

  actionsFor: function (kind) {
    let score = SCORE_ACTIONS.filter(function (action) {
      return kind === "" || INPUT_OPS[action[0]].includes(kind);
    });
    // Only the operations some element of the game takes from a device: a game without timers offers no timer ops.
    const usable = score.filter(function (action) {
      return BindingsPage.elementsFor(action[0], kind).length > 0;
    });
    if (usable.length > 0) {
      score = usable;
    }
    const field = kind === "" || kind === "digital" ? FIELD_ACTIONS : [];
    return {score: score, field: field};
  },

  devicesWith: function (dir) {
    return DevicePages.devices().filter(function (info) {
      if (dir === "out" && info.builtIn) {
        return false; // The PLC's coils are the arena's.
      }
      return !info.manifest || (info.manifest.channels || []).some(function (channel) { return channel.dir === dir; });
    });
  },

  channelOf: function (binding) {
    const channel = DevicePages.channel(binding.device, binding.channel);
    const dir = binding.action ? "in" : "out";
    return channel !== null && channel.dir === dir ? channel : null;
  },

  uniqueId: function (base, except) {
    base = String(base || "binding").replace(/[^A-Za-z0-9_.-]+/g, "-").replace(/^-+|-+$/g, "").slice(0, 60) ||
      "binding";
    const taken = BindingsPage.set.inputs.concat(BindingsPage.set.outputs).filter(function (binding) {
      return binding !== except;
    }).map(function (binding) { return binding.id; });
    let id = base;
    for (let n = 2; taken.includes(id); n++) {
      id = base + "-" + n;
    }
    return id;
  },

  bindingOf: function (card) {
    const key = parseInt(card.attr("data-key"));
    return BindingsPage.set.inputs.concat(BindingsPage.set.outputs).find(function (binding) {
      return binding._key === key;
    }) || null;
  },

  // ---------------------------------------------------------------------------------------------------------------
  // Keeping a binding consistent as its parts change

  // Makes an input binding's fields fit its channel and action, choosing sensible defaults for what is missing, so
  // that the common case is right without touching the details.
  normaliseInput: function (binding) {
    const channel = BindingsPage.channelOf(binding);
    if (channel === null || (channel.type !== "analog" && channel.type !== "encoder")) {
      delete binding.scale;
      delete binding.offset;
      delete binding.threshold;
      delete binding.hysteresis;
    }
    const kind = BindingsPage.inputKind(binding, channel);
    const action = binding.action = binding.action || {};
    const allowed = BindingsPage.actionsFor(kind);
    const isScore = action.field === undefined || action.field === "";
    const valid = isScore ? allowed.score.some(function (a) { return a[0] === action.op; }) :
      allowed.field.some(function (a) { return a[0] === action.field; });
    if (!valid) {
      const op = kind === "analog" || kind === "encoder" || kind === "text" ? "set" : "increment";
      binding.action = {score: action.score, op: op};
    }
    const current = binding.action;
    if (current.field) {
      ["score", "op", "value", "alliance", "robot"].forEach(function (key) { delete current[key]; });
      if (current.field === "estop" || current.field === "astop") {
        if (!BindingsPage.stations.includes(current.station)) {
          current.station = BindingsPage.stations[0] || "";
        }
      } else {
        delete current.station;
      }
    } else {
      delete current.station;
      const elements = BindingsPage.elementsFor(current.op, kind);
      if (!elements.some(function (element) { return element.id === current.score; }) && elements.length > 0) {
        current.score = elements[0].id;
      }
      const element = GameView.isReady() ? GameView.element(current.score) : null;
      if (element !== null) {
        if (element.scope === "field") {
          delete current.alliance;
          delete current.robot;
        } else {
          if (!GameView.alliance(current.alliance)) {
            current.alliance = GameView.alliances().length > 0 ? GameView.alliances()[0].id : "";
          }
          if (element.scope === "robot") {
            if (!(current.robot >= 1 && current.robot <= GameView.view.allianceSize)) {
              current.robot = 1;
            }
          } else {
            delete current.robot;
          }
        }
      }
      BindingsPage.normaliseValue(current, element, kind);
    }
    const stop = STOP_ACTIONS.includes(current.field);
    if (stop || current.op === "hold" || (kind !== "digital" && kind !== "")) {
      delete binding.edge;
    }
    if (stop) {
      delete binding.min_interval_ms;
      delete binding.phases;
    }
    if (kind !== "digital" && kind !== "") {
      delete binding.invert;
      delete binding.debounce_ms;
    }
    if (channel !== null && channel.safety) {
      delete binding.invert;
    }
    if (kind === "counter") {
      delete binding.min_interval_ms;
    }
    if (binding._autoId) {
      binding.id = BindingsPage.uniqueId(binding.device + "-" + binding.channel, binding);
    }
  },

  normaliseValue: function (action, element, kind) {
    const op = action.op;
    if ((op === "increment" || op === "decrement") && kind === "counter") {
      delete action.value;
    } else if (op === "increment" || op === "decrement") {
      if (action.value !== undefined && !(typeof action.value === "number" && action.value > 0)) {
        delete action.value;
      }
    } else if (op === "set" && (kind === "digital" || kind === "")) {
      if (element === null) {
        return;
      }
      if (element.type === "boolean" && typeof action.value !== "boolean") {
        action.value = true;
      } else if (element.type === "enum" &&
        !(element.options || []).some(function (option) { return option.id === action.value; })) {
        action.value = element.options && element.options.length > 0 ? element.options[0].id : "";
      } else if (!["boolean", "enum"].includes(element.type) && typeof action.value !== "number") {
        action.value = 1;
      }
    } else {
      delete action.value;
    }
  },

  normaliseOutput: function (binding) {
    const channel = BindingsPage.channelOf(binding);
    const type = channel ? channel.type : "";
    if (binding.alliance && GameView.isReady() && !GameView.alliance(binding.alliance)) {
      delete binding.alliance;
    }
    if (type === "sound") {
      delete binding.value;
      delete binding.else;
      delete binding.pulse_ms;
      binding.look = {play: (binding.look && binding.look.play) || ""};
      if (!binding.on && !binding.when) {
        binding.on = "match.start";
      }
    } else if (type === "display") {
      delete binding.look;
      delete binding.else;
      if (!binding.value) {
        binding.value = "match_remaining";
      }
    } else if (type === "led_strip") {
      delete binding.value;
      if (!binding.look || binding.look.play !== undefined && binding.look.p === undefined) {
        binding.look = {p: "solid", c: [binding.alliance ? "alliance" : "#00c853"]};
      }
    } else if (type === "digital" || type === "relay" || type === "pwm") {
      ["look", "else"].forEach(function (which) {
        const look = binding[which];
        if (look) {
          binding[which] = {p: ["off", "solid", "blink"].includes(look.p) ? look.p : "solid"};
          if (look.p === "blink" && look.ms) {
            binding[which].ms = look.ms;
          }
        }
      });
      if (type !== "pwm") {
        delete binding.value;
      }
      if (!binding.when && !binding.on && !binding.value && !binding.look) {
        binding.when = "match_state == 'running'";
      }
    }
    if (binding.on) {
      delete binding.when;
      if (type !== "sound" && !(binding.pulse_ms > 0)) {
        binding.pulse_ms = 1000;
      }
    } else {
      delete binding.pulse_ms;
    }
    if (binding._autoId) {
      binding.id = BindingsPage.uniqueId(binding.device + "-" + binding.channel, binding);
    }
  },

  // ---------------------------------------------------------------------------------------------------------------
  // Adding and changing bindings

  // The device a new binding most likely means: the newest approved, connected one with a channel of that direction.
  likelyDevice: function (dir) {
    const devices = BindingsPage.devicesWith(dir).filter(function (info) { return info.manifest; });
    const score = function (info) {
      return (info.state === "approved" ? 2 : 0) + (info.online ? 1 : 0);
    };
    devices.sort(function (a, b) {
      return score(b) - score(a) || (b.createdAt || "").localeCompare(a.createdAt || "");
    });
    return devices[0] || null;
  },

  addInput: function () {
    const info = BindingsPage.likelyDevice("in");
    const channel = info ? info.manifest.channels.find(function (c) { return c.dir === "in"; }) : null;
    const binding = {
      _key: BindingsPage.nextKey++, _autoId: true, device: info ? info.id : "", channel: channel ? channel.id : "",
      action: {},
    };
    BindingsPage.set.inputs.push(binding);
    BindingsPage.normaliseInput(binding);
    BindingsPage.renderCards();
    BindingsPage.changed();
    $(".binding-card[data-key='" + binding._key + "']")[0].scrollIntoView({block: "center"});
  },

  addOutput: function () {
    const info = BindingsPage.likelyDevice("out");
    const channel = info ? info.manifest.channels.find(function (c) { return c.dir === "out"; }) : null;
    const binding = {
      _key: BindingsPage.nextKey++, _autoId: true, device: info ? info.id : "", channel: channel ? channel.id : "",
    };
    if (channel && channel.type === "led_strip" && GameView.alliances().length > 0) {
      binding.alliance = GameView.alliances()[0].id;
    }
    if (!channel || channel.type !== "sound") {
      // Most outputs follow the match; a condition is easier to change than to write.
      binding.when = "match_state == 'running'";
    }
    BindingsPage.set.outputs.push(binding);
    BindingsPage.normaliseOutput(binding);
    BindingsPage.renderCards();
    BindingsPage.changed();
    $(".binding-card[data-key='" + binding._key + "']")[0].scrollIntoView({block: "center"});
  },

  // A select, checkbox or number changed: the binding is updated and its card drawn again.
  fieldChanged: function () {
    const element = $(this);
    const card = element.closest(".binding-card");
    const binding = BindingsPage.bindingOf(card);
    if (binding === null) {
      return;
    }
    const field = element.attr("data-field");
    let value = element.is(":checkbox") ? element.prop("checked") : element.val();
    if (element.attr("type") === "number") {
      value = value === "" ? undefined : Number(value);
    }
    const isInput = binding.action !== undefined;
    if (isInput) {
      BindingsPage.setInputField(binding, field, value, element);
      BindingsPage.normaliseInput(binding);
    } else {
      BindingsPage.setOutputField(binding, field, value, element);
      BindingsPage.normaliseOutput(binding);
    }
    BindingsPage.redrawCard(card, binding);
    BindingsPage.changed();
  },

  // An expression or name is being typed: the binding follows without drawing the card again.
  textChanged: function () {
    const element = $(this);
    const binding = BindingsPage.bindingOf(element.closest(".binding-card"));
    if (binding === null) {
      return;
    }
    const field = element.attr("data-text");
    const value = element.val();
    if (field === "id") {
      binding.id = value;
      binding._autoId = false;
    } else if (field === "when" || field === "value") {
      binding[field] = value;
    } else if (field === "play") {
      binding.look = {play: value};
    } else if (field === "look.v" || field === "else.v") {
      const which = field.split(".")[0];
      binding[which] = binding[which] || {};
      binding[which].v = value;
    }
    BindingsPage.changed();
  },

  setInputField: function (binding, field, value, element) {
    const action = binding.action;
    switch (field) {
      case "device":
        if (value === "\u0000other") {
          value = prompt("The id of the device (lowercase letters, digits and dashes):", "") || binding.device;
        }
        if (value !== binding.device) {
          binding.device = value;
          const info = DevicePages.device(value);
          const first = info && info.manifest ? (info.manifest.channels || []).find(function (c) {
            return c.dir === "in";
          }) : null;
          binding.channel = first ? first.id : "";
        }
        break;
      case "channel":
        binding.channel = value;
        break;
      case "edge":
        binding.edge = value === "rising" ? undefined : value;
        break;
      case "do": {
        const parts = value.split(":");
        binding.action = parts[0] === "field" ? {field: parts[1]} : {
          op: parts[1], score: action.score, alliance: action.alliance, robot: action.robot,
        };
        break;
      }
      case "element":
        action.score = value;
        delete action.value;
        break;
      case "amount":
        action.value = value === undefined || value === 1 ? undefined : value;
        break;
      case "setValue": {
        const element = GameView.element(action.score);
        if (element && element.type === "boolean") {
          action.value = value === "true";
        } else if (element && element.type === "enum") {
          action.value = value;
        } else {
          action.value = value;
        }
        break;
      }
      case "alliance":
        action.alliance = value;
        break;
      case "robot":
        action.robot = parseInt(value);
        break;
      case "station":
        action.station = value;
        break;
      case "phase": {
        const phases = (binding.phases || []).filter(function (phase) { return phase !== element.attr("data-phase"); });
        if (value) {
          phases.push(element.attr("data-phase"));
        }
        // Kept in the game's order.
        binding.phases = GameView.phases().map(function (phase) { return phase.id; }).filter(function (id) {
          return phases.includes(id);
        });
        break;
      }
      case "invert":
        binding.invert = value ? true : undefined;
        break;
      case "debounce_ms":
      case "min_interval_ms":
        binding[field] = value > 0 ? Math.round(value) : undefined;
        break;
      case "scale":
      case "offset":
      case "threshold":
      case "hysteresis":
        binding[field] = typeof value === "number" && !isNaN(value) ? value : undefined;
        break;
    }
  },

  setOutputField: function (binding, field, value, element) {
    switch (field) {
      case "device":
        if (value === "\u0000other") {
          value = prompt("The id of the device (lowercase letters, digits and dashes):", "") || binding.device;
        }
        if (value !== binding.device) {
          binding.device = value;
          const info = DevicePages.device(value);
          const first = info && info.manifest ? (info.manifest.channels || []).find(function (c) {
            return c.dir === "out";
          }) : null;
          binding.channel = first ? first.id : "";
          delete binding.look;
          delete binding.else;
        }
        break;
      case "channel":
        if (value !== binding.channel) {
          binding.channel = value;
          delete binding.look;
          delete binding.else;
        }
        break;
      case "mode":
        if (value === "event") {
          binding.on = binding.on || "match.start";
          delete binding.when;
        } else if (value === "condition") {
          delete binding.on;
          binding.when = binding.when || "match_state == 'running'";
        } else {
          delete binding.on;
          delete binding.when;
          const channel = BindingsPage.channelOf(binding);
          if (channel && ["digital", "relay", "pwm"].includes(channel.type) && !binding.look && !binding.value) {
            binding.look = {p: "solid"};
          }
        }
        break;
      case "on":
        binding.on = value;
        break;
      case "pulse_ms":
        binding.pulse_ms = value > 0 ? Math.round(value) : undefined;
        break;
      case "alliance":
        binding.alliance = value || undefined;
        if (!binding.alliance) {
          // The colour "alliance" needs one.
          ["look", "else"].forEach(function (which) {
            if (binding[which] && binding[which].c) {
              binding[which].c = binding[which].c.map(function (c) { return c === "alliance" ? "#ffffff" : c; });
            }
          });
        }
        break;
      case "insert-when":
      case "insert-value":
      case "insert-v": {
        if (value === "") {
          break;
        }
        const target = {"insert-when": "when", "insert-value": "value", "insert-v": "look.v"}[field];
        if (target === "look.v") {
          binding.look = binding.look || {};
          binding.look.v = value;
        } else {
          binding[target] = value;
        }
        break;
      }
      case "else-enabled":
        if (value) {
          const channel = BindingsPage.channelOf(binding);
          binding.else = channel && channel.type === "led_strip" ? {p: "solid", c: ["#202020"]} : {p: "solid"};
        } else {
          delete binding.else;
        }
        break;
      default:
        if (field.startsWith("look.") || field.startsWith("else.")) {
          BindingsPage.setLookField(binding, field, value, element);
        }
    }
  },

  setLookField: function (binding, field, value, element) {
    const which = field.split(".")[0];
    const part = field.split(".")[1];
    const look = binding[which] = binding[which] || {};
    switch (part) {
      case "p":
        look.p = value;
        if (!TIMED_PATTERNS.includes(value)) {
          delete look.ms;
        }
        if (value !== "progress") {
          delete look.v;
        } else if (!look.v) {
          look.v = "phase_remaining / phase_duration";
        }
        break;
      case "ms":
        look.ms = value > 0 ? Math.round(value) : undefined;
        break;
      case "br":
        look.br = value === undefined || value >= 100 ? undefined : Math.max(0, value) / 100;
        break;
      case "ckind":
      case "color": {
        const index = parseInt(element.attr("data-index"));
        const colors = (look.c || []).slice();
        const kind = element.closest(".color-slot").find("[data-color-kind]").val();
        const picked = element.closest(".color-slot").find("input[type=color]").val();
        if (kind === "none") {
          colors.splice(index);
        } else {
          colors[index] = kind === "alliance" ? "alliance" : picked;
          for (let i = 0; i < index; i++) {
            colors[i] = colors[i] || "#000000";
          }
        }
        look.c = colors;
        break;
      }
    }
  },

  buttonClicked: function () {
    const button = $(this);
    const card = button.closest(".binding-card");
    const binding = BindingsPage.bindingOf(card);
    if (binding === null) {
      return;
    }
    const list = binding.action !== undefined ? BindingsPage.set.inputs : BindingsPage.set.outputs;
    switch (button.attr("data-button")) {
      case "remove":
        list.splice(list.indexOf(binding), 1);
        BindingsPage.renderCards();
        break;
      case "duplicate": {
        const copy = JSON.parse(JSON.stringify(binding));
        copy._key = BindingsPage.nextKey++;
        copy._autoId = false;
        copy.id = BindingsPage.uniqueId(binding.id, copy);
        list.splice(list.indexOf(binding) + 1, 0, copy);
        BindingsPage.renderCards();
        break;
      }
      case "capture-threshold":
      case "capture-offset": {
        const input = BindingsPage.liveInput(binding);
        if (!input || typeof input.value !== "number") {
          DevicePages.notify("There is no reading from " + binding.device + "." + binding.channel + " yet.", "warning");
          return;
        }
        const scale = typeof binding.scale === "number" ? binding.scale : 1;
        if (button.attr("data-button") === "capture-threshold") {
          const offset = typeof binding.offset === "number" ? binding.offset : 0;
          binding.threshold = Math.round((input.value * scale + offset) * 1000) / 1000;
        } else {
          binding.offset = Math.round(-input.value * scale * 1000) / 1000;
        }
        BindingsPage.normaliseInput(binding);
        BindingsPage.redrawCard(card, binding);
        break;
      }
    }
    BindingsPage.changed();
  },

  // ---------------------------------------------------------------------------------------------------------------
  // Drawing

  renderCards: function () {
    if ($(".binding-card :focus").length > 0) {
      // Drawing would take the focus from what is being typed; it is done when the focus leaves.
      BindingsPage.renderPending = true;
      return;
    }
    BindingsPage.renderPending = false;
    $("#gameName").text(GameView.isReady() ? GameView.view.name : BindingsPage.game);
    const inputs = $("#inputBindings").empty();
    const outputs = $("#outputBindings").empty();
    if (!GameView.isReady()) {
      return;
    }
    BindingsPage.set.inputs.forEach(function (binding) {
      inputs.append(BindingsPage.inputCard(binding));
    });
    if (BindingsPage.set.inputs.length === 0) {
      inputs.append("<div class='text-secondary small'>No input bindings yet. Click <b>Add input binding</b>: it " +
        "starts with the newest device and a sensible action, ready to adjust.</div>");
    }
    BindingsPage.set.outputs.forEach(function (binding) {
      outputs.append(BindingsPage.outputCard(binding));
    });
    if (BindingsPage.set.outputs.length === 0) {
      outputs.append("<div class='text-secondary small'>No output bindings.</div>");
    }
    BindingsPage.showProblems();
    BindingsPage.updateLive();
  },

  redrawCard: function (card, binding) {
    const fresh = binding.action !== undefined ? BindingsPage.inputCard(binding) : BindingsPage.outputCard(binding);
    card.replaceWith(fresh);
    BindingsPage.showProblems();
    BindingsPage.updateLive();
  },

  select: function (field, options, selected, extra) {
    const e = DevicePages.escape;
    let html = "<select class='form-select form-select-sm' data-field='" + field + "'" + (extra || "") + ">";
    options.forEach(function (option) {
      if (option.group !== undefined) {
        html += option.group ? "<optgroup label='" + e(option.group) + "'>" : "</optgroup>";
        return;
      }
      const isSelected = String(option.value) === String(selected);
      html += "<option value='" + e(option.value) + "'" + (isSelected ? " selected" : "") + ">" + e(option.label) +
        "</option>";
    });
    return html + "</select>";
  },

  word: function (text) {
    return "<span class='word'>" + DevicePages.escape(text) + "</span>";
  },

  deviceOptions: function (binding, dir) {
    const options = BindingsPage.devicesWith(dir).map(function (info) {
      const state = info.state === "approved" ? (info.online ? "" : " – offline") : " – " + info.state;
      return {value: info.id, label: (info.name !== info.id ? info.name + " (" + info.id + ")" : info.id) + state};
    });
    if (binding.device && !options.some(function (option) { return option.value === binding.device; })) {
      options.unshift({value: binding.device, label: binding.device + " (not connected yet)"});
    }
    if (!binding.device) {
      options.unshift({value: "", label: "choose a device"});
    }
    options.push({value: "\u0000other", label: "another device id…"});
    return options;
  },

  channelControl: function (binding, dir) {
    const info = DevicePages.device(binding.device);
    if (info === null || !info.manifest) {
      return "<input type='text' class='form-control form-control-sm' style='width: 9rem' data-field='channel' " +
        "placeholder='channel id' value='" + DevicePages.escape(binding.channel) + "'>";
    }
    const options = (info.manifest.channels || []).filter(function (channel) { return channel.dir === dir; })
      .map(function (channel) {
        return {value: channel.id, label: channel.id + (channel.name ? " – " + channel.name : "") + " (" +
          channel.type + ")"};
      });
    if (binding.channel && !options.some(function (option) { return option.value === binding.channel; })) {
      options.unshift({value: binding.channel, label: binding.channel + " (not in its manifest)"});
    }
    return BindingsPage.select("channel", options, binding.channel);
  },

  cardHeader: function (binding, kind) {
    return "<div class='d-flex align-items-center gap-2 mb-2'><span class='badge text-bg-secondary'>" + kind +
      "</span><code class='small'>" + DevicePages.escape(binding.id) + "</code><span class='ms-auto'></span>" +
      "<button type='button' class='btn btn-sm btn-outline-secondary' data-button='duplicate'>Duplicate</button>" +
      "<button type='button' class='btn btn-sm btn-outline-danger' data-button='remove' title='Remove'>" +
      "<i class='bi bi-trash'></i></button></div>";
  },

  inputCard: function (binding) {
    const e = DevicePages.escape;
    const channel = BindingsPage.channelOf(binding);
    const kind = BindingsPage.inputKind(binding, channel);
    const action = binding.action;
    const stop = STOP_ACTIONS.includes(action.field);
    let html = "<div class='card binding-card mb-2' data-key='" + binding._key + "'><div class='card-body py-2'>" +
      BindingsPage.cardHeader(binding, "input") + "<div class='binding-sentence'>" + BindingsPage.word("When") +
      BindingsPage.select("device", BindingsPage.deviceOptions(binding, "in"), binding.device) +
      BindingsPage.word(".") + BindingsPage.channelControl(binding, "in");

    // What of the input triggers it.
    if (stop) {
      html += BindingsPage.word(channel && channel.safety ? "opens (reads 0)" : "is on") + BindingsPage.word(",");
    } else if (action.op === "hold") {
      html += BindingsPage.word("is on") + BindingsPage.word(",");
    } else if (kind === "digital" || kind === "") {
      html += BindingsPage.select("edge", [
        {value: "rising", label: "turns on"}, {value: "falling", label: "turns off"},
        {value: "both", label: "changes"},
      ], binding.edge || "rising") + BindingsPage.word(",");
    } else {
      html += BindingsPage.word({counter: "counts", text: "reads a tag"}[kind] || "reads a value") +
        BindingsPage.word(",");
    }

    // What it does.
    const actions = BindingsPage.actionsFor(kind);
    const doOptions = [{group: "Score"}].concat(actions.score.map(function (a) {
      return {value: "score:" + a[0], label: a[1]};
    }), [{group: ""}]);
    if (actions.field.length > 0) {
      doOptions.push({group: "Field"});
      actions.field.forEach(function (a) { doOptions.push({value: "field:" + a[0], label: a[1]}); });
      doOptions.push({group: ""});
    }
    html += BindingsPage.select("do", doOptions, action.field ? "field:" + action.field : "score:" + action.op);

    if (action.field) {
      if (action.field === "estop" || action.field === "astop") {
        html += BindingsPage.select("station", BindingsPage.stations.map(function (station) {
          return {value: station, label: station};
        }), action.station);
      }
    } else {
      const element = GameView.element(action.score);
      if ((action.op === "increment" || action.op === "decrement") && kind !== "counter") {
        html += "<input type='number' class='form-control form-control-sm' style='width: 4.5rem' min='0' step='any' " +
          "data-field='amount' title='How much each time' value='" + e(action.value === undefined ? 1 : action.value) +
          "'>";
      } else if ((action.op === "increment" || action.op === "decrement")) {
        html += BindingsPage.word("the count");
      }
      if (ACTION_LINKS[action.op]) {
        html += BindingsPage.word(ACTION_LINKS[action.op]);
      }
      const elements = BindingsPage.elementsFor(action.op, kind).map(function (candidate) {
        return {value: candidate.id, label: candidate.name + " (" + candidate.type + ", " + candidate.scope + ")"};
      });
      if (action.score && !elements.some(function (option) { return option.value === action.score; })) {
        elements.unshift({value: action.score, label: action.score + " (cannot be changed this way)"});
      }
      if (elements.length === 0) {
        html += "<span class='text-warning small'>The game has no element devices can change this way.</span>";
      } else {
        html += BindingsPage.select("element", elements, action.score);
      }
      if (action.op === "set" && element !== null) {
        if (kind === "digital" || kind === "") {
          html += BindingsPage.word("to");
          if (element.type === "boolean") {
            html += BindingsPage.select("setValue", [{value: "true", label: "true"}, {value: "false", label: "false"}],
              String(action.value));
          } else if (element.type === "enum") {
            html += BindingsPage.select("setValue", (element.options || []).map(function (option) {
              return {value: option.id, label: option.name};
            }), action.value);
          } else {
            html += "<input type='number' class='form-control form-control-sm' style='width: 5.5rem' step='any' " +
              "data-field='setValue' value='" + e(action.value) + "'>";
          }
        } else {
          html += BindingsPage.word(kind === "text" ? "to the option it reads" : "to the reading");
        }
      }
      if (element !== null && element.scope !== "field") {
        html += BindingsPage.word("for") + BindingsPage.select("alliance", GameView.alliances().map(function (a) {
          return {value: a.id, label: a.name};
        }), action.alliance);
        if (element.scope === "robot") {
          const robots = [];
          for (let robot = 1; robot <= GameView.view.allianceSize; robot++) {
            robots.push({value: robot, label: "robot " + robot});
          }
          html += BindingsPage.select("robot", robots, action.robot);
        }
      }
    }
    html += "</div>";
    html += "<div class='binding-live mt-1' data-live-key='" + binding._key + "'></div>";
    html += BindingsPage.inputDetails(binding, channel, kind, stop);
    return html + "<div class='binding-problems'></div></div></div>";
  },

  inputDetails: function (binding, channel, kind, stop) {
    const e = DevicePages.escape;
    const number = function (field, label, value, title) {
      return "<div class='col-auto'><label>" + label + "</label><input type='number' step='any' " +
        "class='form-control form-control-sm' style='width: 7rem' data-field='" + field + "' value='" +
        e(value === undefined ? "" : value) + "'" + (title ? " title='" + e(title) + "'" : "") + "></div>";
    };
    const open = binding.phases || binding.invert || binding.debounce_ms || binding.min_interval_ms ||
      binding.scale !== undefined || binding.offset !== undefined || binding.threshold !== undefined;
    let html = "<details class='binding-more mt-1'" + (open ? " open" : "") + ">" +
      "<summary class='small text-secondary'>More options</summary><div class='row g-2 mt-1 align-items-end'>";
    html += "<div class='col-auto'><label>Binding id</label><input type='text' class='form-control form-control-sm' " +
      "data-text='id' value='" + e(binding.id) + "'></div>";
    if (!stop) {
      html += "<div class='col-auto'><label>Only during</label><div>";
      GameView.phases().forEach(function (phase) {
        const checked = (binding.phases || []).includes(phase.id);
        html += "<label class='form-check form-check-inline mb-0'><input type='checkbox' class='form-check-input' " +
          "data-field='phase' data-phase='" + e(phase.id) + "'" + (checked ? " checked" : "") + "> " +
          e(phase.name) + "</label>";
      });
      html += "</div><div class='form-text mt-0'>None ticked: any time.</div></div>";
    }
    if ((kind === "digital" || kind === "") && !(channel && channel.safety)) {
      html += "<div class='col-auto'><label class='form-check mb-1'><input type='checkbox' class='form-check-input' " +
        "data-field='invert'" + (binding.invert ? " checked" : "") + "> Invert (0 is on)</label></div>";
    }
    if (kind === "digital" || kind === "") {
      html += number("debounce_ms", "Debounce (ms)", binding.debounce_ms,
        "The input must hold its new value this long");
    }
    if (!stop && kind !== "counter") {
      html += number("min_interval_ms", "Min interval (ms)", binding.min_interval_ms,
        "Ignore further triggers for this long after one");
    }
    if (channel !== null && (channel.type === "analog" || channel.type === "encoder")) {
      html += number("scale", "Scale", binding.scale, "value = reading × scale + offset") +
        number("offset", "Offset", binding.offset) +
        "<div class='col-auto'><button type='button' class='btn btn-sm btn-outline-info' " +
        "data-button='capture-offset' title='Set the offset so that the current reading is 0'>Zero here</button>" +
        "</div>" +
        number("threshold", "Threshold", binding.threshold, "Act as a digital input: on at this value or above") +
        "<div class='col-auto'><button type='button' class='btn btn-sm btn-outline-info' " +
        "data-button='capture-threshold'>Use current reading</button></div>";
      if (binding.threshold !== undefined) {
        html += number("hysteresis", "Hysteresis", binding.hysteresis, "Turns off below threshold − hysteresis");
      }
    }
    return html + "</div></details>";
  },

  outputMode: function (binding) {
    if (binding.on) {
      return "event";
    }
    return binding.when ? "condition" : "always";
  },

  conditionPicks: function (binding) {
    const picks = [{value: "", label: "pick…"}, {value: "match_state == 'running'", label: "the match is running"},
      {value: "match_state == 'post'", label: "the match has ended"}];
    GameView.phases().forEach(function (phase) {
      picks.push({value: "phase == '" + phase.id + "'", label: "during " + phase.name});
    });
    picks.push({value: "field_reset", label: "field reset is signalled"},
      {value: "volunteers", label: "volunteers are called"}, {value: "fault", label: "a device has a fault"});
    (GameView.view.live || []).forEach(function (live) {
      const target = live.scope === "field" ? "field." + live.id : (binding.alliance ? "own." + live.id :
        (GameView.alliances()[0] ? GameView.alliances()[0].id + "." + live.id : live.id));
      picks.push({value: target, label: live.name + " (" + target + ")"});
    });
    return picks;
  },

  eventOptions: function () {
    const options = [{value: "match.start", label: "the match starts"}, {value: "match.end", label: "the match ends"},
      {value: "match.abort", label: "the match is aborted"}, {value: "score.posted", label: "the score is posted"}];
    GameView.phases().forEach(function (phase) {
      options.push({value: "phase." + phase.id + ".start", label: phase.name + " starts"});
      options.push({value: "phase." + phase.id + ".end", label: phase.name + " ends"});
      (phase.cues || []).forEach(function (cue) {
        if (cue.id) {
          options.push({value: "cue." + cue.id, label: "cue " + cue.id + " (" + phase.name + ")"});
        }
      });
    });
    return options;
  },

  outputCard: function (binding) {
    const e = DevicePages.escape;
    const channel = BindingsPage.channelOf(binding);
    const type = channel ? channel.type : "";
    const mode = BindingsPage.outputMode(binding);
    let html = "<div class='card binding-card mb-2' data-key='" + binding._key + "'><div class='card-body py-2'>" +
      BindingsPage.cardHeader(binding, "output") + "<div class='binding-sentence'>" + BindingsPage.word("Output") +
      BindingsPage.select("device", BindingsPage.deviceOptions(binding, "out"), binding.device) +
      BindingsPage.word(".") + BindingsPage.channelControl(binding, "out");
    if (channel !== null) {
      html += "<span class='badge text-bg-dark'>" + e(type) + (channel.pixels ? " · " + channel.pixels + " px" :
        "") + "</span><span class='ms-2 small text-secondary'>now:</span> " +
        DevicePages.outputHtml(binding.device, channel);
    }
    html += "</div><div class='binding-sentence mt-2'>";
    const modes = [{value: "condition", label: "while a condition holds"},
      {value: "event", label: "for a moment, when something happens"}];
    if (type !== "sound") {
      modes.splice(1, 0, {value: "always", label: "all the time"});
    }
    html += BindingsPage.word(type === "sound" ? "Play" : "Show") + BindingsPage.select("mode", modes, mode);
    if (mode === "condition") {
      html += "<input type='text' class='form-control form-control-sm font-monospace' style='width: 22rem' " +
        "data-text='when' value='" + e(binding.when) + "' title='An expression; see the game definition reference'>" +
        BindingsPage.select("insert-when", BindingsPage.conditionPicks(binding), "", " title='Insert a condition'");
    } else if (mode === "event") {
      html += BindingsPage.word("when") + BindingsPage.select("on", BindingsPage.eventOptions(), binding.on);
      if (type !== "sound") {
        html += BindingsPage.word("for") + "<input type='number' class='form-control form-control-sm' " +
          "style='width: 6rem' min='1' data-field='pulse_ms' value='" + e(binding.pulse_ms) + "'>" +
          BindingsPage.word("ms");
      }
    }
    html += "</div><div class='mt-2'>" + BindingsPage.outputLooks(binding, type, mode) + "</div>";
    html += "<div class='binding-sentence mt-2 small'>" + BindingsPage.word("For alliance") +
      BindingsPage.select("alliance", [{value: "", label: "none"}].concat(GameView.alliances().map(function (a) {
        return {value: a.id, label: a.name};
      })), binding.alliance || "") + "<span class='text-secondary'>(sets <code>own</code> in expressions and the " +
      "colour <i>alliance</i>)</span></div>";
    html += "<details class='binding-more mt-1'><summary class='small text-secondary'>More options</summary>" +
      "<div class='row g-2 mt-1'><div class='col-auto'><label>Binding id</label><input type='text' " +
      "class='form-control form-control-sm' data-text='id' value='" + e(binding.id) + "'></div></div></details>";
    return html + "<div class='binding-problems'></div></div></div>";
  },

  outputLooks: function (binding, type, mode) {
    const e = DevicePages.escape;
    let html = "";
    switch (type) {
      case "led_strip":
        html += "<div class='look-editor'><span class='word'>Look</span>" + BindingsPage.lookEditor(binding, "look") +
          "</div>";
        if (mode !== "always") {
          html += "<div class='look-editor mt-1'><label class='form-check mb-0'><input type='checkbox' " +
            "class='form-check-input' data-field='else-enabled'" + (binding.else ? " checked" : "") +
            "> Otherwise</label>" + (binding.else ? BindingsPage.lookEditor(binding, "else") :
            "<span class='small text-secondary'>off</span>") + "</div>";
        }
        break;
      case "digital":
      case "relay":
      case "pwm":
        if (type === "pwm") {
          html += "<div class='look-editor'><span class='word'>Level</span><input type='text' " +
            "class='form-control form-control-sm font-monospace' style='width: 14rem' data-text='value' " +
            "placeholder='1 (full)' value='" + e(binding.value) + "' title='An expression from 0 to 1'></div>";
        }
        html += "<div class='look-editor mt-1'><span class='word'>On as</span>" +
          BindingsPage.simpleLook(binding, "look", ["solid", "blink"]) + "</div>";
        if (mode !== "always") {
          html += "<div class='look-editor mt-1'><label class='form-check mb-0'><input type='checkbox' " +
            "class='form-check-input' data-field='else-enabled'" + (binding.else ? " checked" : "") +
            "> Otherwise</label>" + (binding.else ?
            BindingsPage.simpleLook(binding, "else", ["solid", "blink", "off"]) :
            "<span class='small text-secondary'>off</span>") + "</div>";
        }
        break;
      case "display":
        html += "<div class='look-editor'><span class='word'>Text or number</span><input type='text' " +
          "class='form-control form-control-sm font-monospace' style='width: 18rem' data-text='value' value='" +
          e(binding.value) + "'>" + BindingsPage.select("insert-value", [{value: "", label: "pick…"},
          {value: "match_remaining", label: "time left in the match"},
          {value: "phase_remaining", label: "time left in the phase"}].concat(GameView.alliances().map(function (a) {
          return {value: a.id + ".score", label: a.name + " score"};
        })), "") + "</div>";
        break;
      case "sound":
        html += "<div class='look-editor'><span class='word'>Sound</span><input type='text' " +
          "class='form-control form-control-sm' style='width: 12rem' data-text='play' placeholder='sound name' " +
          "value='" + e(binding.look ? binding.look.play : "") + "'></div>";
        break;
      default:
        html += "<div class='small text-secondary'>Choose a device and channel; the options follow its type.</div>";
    }
    return html;
  },

  simpleLook: function (binding, which, patterns) {
    const look = binding[which] || {p: "solid"};
    let html = BindingsPage.select(which + ".p", patterns.map(function (pattern) {
      return {value: pattern, label: {solid: "steady", blink: "blinking", off: "off"}[pattern]};
    }), look.p || "solid");
    if (look.p === "blink") {
      html += BindingsPage.word("every") + "<input type='number' class='form-control form-control-sm' " +
        "style='width: 6rem' min='0' step='50' data-field='" + which + ".ms' value='" + (look.ms || 1000) + "'>" +
        BindingsPage.word("ms");
    }
    return html;
  },

  lookEditor: function (binding, which) {
    const e = DevicePages.escape;
    const look = binding[which] || {};
    if (look.seg && look.seg.length > 0) {
      return "<span class='small text-warning'>" + look.seg.length + " segments: edit them in the YAML tab.</span>";
    }
    let html = BindingsPage.select(which + ".p", PATTERNS.map(function (pattern) {
      return {value: pattern, label: pattern};
    }), look.p || "solid");
    const colors = look.c || [];
    for (let index = 0; index < 2; index++) {
      const color = colors[index];
      const kind = color === undefined ? "none" : color === "alliance" ? "alliance" : "custom";
      if (index > 0 && colors.length === 0) {
        break;
      }
      const kinds = [{value: "custom", label: index === 0 ? "colour" : "then"},
        {value: "alliance", label: "alliance colour"}];
      if (index > 0 || colors.length === 0) {
        kinds.push({value: "none", label: index === 0 ? "no colour" : "black"});
      }
      html += "<span class='color-slot d-inline-flex gap-1'>" + BindingsPage.select(which + ".ckind", kinds, kind,
        " data-color-kind data-index='" + index + "'") + (kind === "custom" ? "<input type='color' data-field='" +
        which + ".color' data-index='" + index + "' value='" + e(color) + "'>" : "<input type='color' class='d-none' " +
        "value='#ffffff'>") + "</span>";
    }
    if (TIMED_PATTERNS.includes(look.p)) {
      html += BindingsPage.word("period") + "<input type='number' class='form-control form-control-sm' " +
        "style='width: 6rem' min='0' step='50' data-field='" + which + ".ms' value='" + e(look.ms || "") + "' " +
        "placeholder='default'>" + BindingsPage.word("ms");
    }
    html += BindingsPage.word("brightness") + "<input type='number' class='form-control form-control-sm' " +
      "style='width: 5rem' min='0' max='100' data-field='" + which + ".br' value='" +
      (look.br === undefined ? 100 : Math.round(look.br * 100)) + "'>" + BindingsPage.word("%");
    if (look.p === "progress") {
      html += BindingsPage.word("fill") + "<input type='text' class='form-control form-control-sm font-monospace' " +
        "style='width: 16rem' data-text='" + which + ".v' value='" + e(look.v) + "' title='0 to 1: a number or an " +
        "expression'>";
      if (which === "look") {
        html += BindingsPage.select("insert-v", [{value: "", label: "pick…"},
          {value: "phase_remaining / phase_duration", label: "time left in the phase"},
          {value: "match_remaining / " + Math.round(GameView.view.duration), label: "time left in the match"}], "");
      }
    }
    return html;
  },

  // ---------------------------------------------------------------------------------------------------------------
  // Live values

  liveInput: function (binding) {
    const info = DevicePages.device(binding.device);
    return info && info.inputs ? info.inputs[binding.channel] || null : null;
  },

  updateLive: function () {
    BindingsPage.set.inputs.forEach(function (binding) {
      const element = $("[data-live-key='" + binding._key + "']");
      if (element.length === 0) {
        return;
      }
      const info = DevicePages.device(binding.device);
      if (info === null) {
        element.html("Device <code>" + DevicePages.escape(binding.device) + "</code> has not connected yet.");
        return;
      }
      const channel = BindingsPage.channelOf(binding);
      const input = BindingsPage.liveInput(binding);
      let html = (info.online ? "<span class='badge text-bg-success'>online</span>" :
        "<span class='badge text-bg-secondary'>offline</span>") + (info.state !== "approved" ?
        " <span class='badge text-bg-warning'>" + DevicePages.escape(info.state) + "</span>" : "") + " Now: " +
        DevicePages.formatInput(channel, input);
      if (input && typeof input.value === "number" && (binding.scale !== undefined || binding.offset !== undefined)) {
        const scaled = input.value * (binding.scale === undefined ? 1 : binding.scale) +
          (binding.offset === undefined ? 0 : binding.offset);
        html += " → scaled " + Math.round(scaled * 1000) / 1000;
      }
      if (input) {
        html += " <span class='text-secondary'>(" + DevicePages.ago(input.time) + ")</span>";
      }
      element.html(html);
    });
  },

  showLive: function (event) {
    if (event.type === "device.input") {
      BindingsPage.updateLive();
      BindingsPage.set.inputs.forEach(function (binding) {
        if (binding.device === event.data.id && binding.channel === event.data.channel) {
          const element = $("[data-live-key='" + binding._key + "']");
          element.removeClass("input-flash");
          void element[0]?.offsetWidth;
          element.addClass("input-flash");
        }
      });
    }
  },
};

$(function () {
  BindingsPage.start();
});
