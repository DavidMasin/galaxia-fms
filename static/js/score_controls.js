// Builds the controls with which a person changes the scoring elements of the active game. The scoring panel and
// the referee panel share it. Every control is generated from the game view (see game_view.js, which must be loaded
// first) and shows the raw state carried by the "realtimeScore" websocket message; nothing here knows any game.
//
// A control never changes what it shows by itself: it sends a "score" command and waits for the server to send the
// new state, so that every panel always shows the same thing.

const ScoreControls = {
  // Returns the id of the physical field station of the alliance at the given index and robot position (1..n).
  // These name the two sides of the field and stay the same whatever a game calls its alliances.
  stationId: function (allianceIndex, position) {
    return ["R", "B"][allianceIndex] + position;
  },

  // Returns the scoring panel positions of a game: the ones it declares, or else one per alliance, named by the
  // alliance id, plus "field" if the game has anything scored for the field as a whole.
  panels: function (view) {
    if (view.panels.length > 0) {
      return view.panels;
    }
    const panels = view.alliances.map(function (alliance) {
      return {id: alliance.id, name: alliance.name, alliances: [alliance.id], groups: []};
    });
    if (view.elements.some(function (element) { return element.scope === "field"; })) {
      panels.push({id: "field", name: "Field", alliances: [], groups: []});
    }
    return panels;
  },

  // Returns what a panel shows, as a list of sections: one for the field as a whole (allianceId "") if the panel
  // shows field elements, then one per alliance. Each section lists its elements in display order.
  //   An alliance or robot element is shown for each of the panel's alliances (all of them if it names none) if
  //   its group is one of the panel's groups (any group if it names none).
  //   A field element is shown if the panel names no alliances and its group passes, or if the panel names its group.
  panelSections: function (view, panel) {
    const groups = panel.groups || [];
    const allianceIds = (panel.alliances || []).length > 0 ? panel.alliances :
      view.alliances.map(function (alliance) { return alliance.id; });
    const groupShown = function (element) {
      return groups.length === 0 || groups.includes(element.group);
    };
    const visible = view.elements.filter(function (element) { return !element.hidden; })
      .sort(function (a, b) { return a.order - b.order; });

    const sections = [];
    const fieldElements = visible.filter(function (element) {
      return element.scope === "field" &&
        (((panel.alliances || []).length === 0 && groupShown(element)) || groups.includes(element.group));
    });
    if (fieldElements.length > 0) {
      sections.push({allianceId: "", elements: fieldElements});
    }
    const allianceElements = visible.filter(function (element) {
      return element.scope !== "field" && groupShown(element);
    });
    if (allianceElements.length > 0) {
      allianceIds.forEach(function (allianceId) {
        if (GameView.alliance(allianceId) !== null) {
          sections.push({allianceId: allianceId, elements: allianceElements});
        }
      });
    }
    return sections;
  },

  // Returns the raw value of an element for one owner from a realtimeScore message, or undefined if it holds none.
  rawValue: function (score, element, allianceId, robot) {
    if (!score) {
      return undefined;
    }
    if (element.scope === "field") {
      return ((score.field || {}).state || {})[element.id];
    }
    const alliance = (score.alliances || {})[allianceId];
    if (!alliance || !alliance.state) {
      return undefined;
    }
    if (element.scope === "alliance") {
      return (alliance.state.elements || {})[element.id];
    }
    return ((alliance.state.robots || [])[robot - 1] || {})[element.id];
  },

  // Says whether a person in the given role ("scorer" or "referee") may change an element right now. clock is
  // {state, phaseIndex, matchTimeSec}, where state is a value of matchStates.
  //   During the match the element must list the role and accept the running phase, or the phase that has just
  //   ended while its grace window is open: the game's scoreGrace seconds from the moment that phase ended. A
  //   phase in which robots were disabled has no window of its own; the window of the phase before it carries on
  //   through it. After the match everything can be put right. Otherwise nothing can.
  editable: function (element, role, clock) {
    if (clock.state === "POST_MATCH") {
      return true;
    }
    if (clock.state !== "IN_MATCH" || !element.edit.includes(role)) {
      return false;
    }
    const phases = GameView.phases();
    const phase = phases[clock.phaseIndex];
    if (!phase) {
      return false;
    }
    if (element.phases.includes(phase.id)) {
      return true;
    }
    for (let i = clock.phaseIndex - 1; i >= 0; i--) {
      if (clock.matchTimeSec - phases[i + 1].start >= GameView.view.scoreGrace) {
        break;
      }
      if (phases[i].robots !== "disabled") {
        return element.phases.includes(phases[i].id);
      }
    }
    return false;
  },

  // Formats a number of seconds for a timer readout.
  formatSeconds: function (seconds) {
    return (Math.round(seconds * 10) / 10).toFixed(1) + " s";
  },

  // Creates the control for one element and one owner. options is
  //   {element, allianceId, robot, label, send}, where send(command) is given {alliance, robot, element, op, value,
  //   bucket} to pass on to the server as a "score" command.
  // Returns {node, update}; call update(raw, enabled, clock) whenever the score, the clock or the game changes.
  create: function (options) {
    const element = options.element;
    const node = $("<div class='score-control'></div>").attr("data-type", element.type)
      .attr("data-widget", element.widget);
    if (options.label) {
      node.append($("<div class='score-control-label'></div>").text(options.label));
    }
    const body = $("<div class='score-control-body'></div>").appendTo(node);

    // After the match the clock no longer says which phase a change belongs to, so the person does. Counters are
    // kept per phase and always need one; a boolean or an enum needs one only if its points depend on the phase.
    const needsPhase = element.type === "counter" ||
      ((element.type === "boolean" || element.type === "enum") && element.phasePoints === true);
    const phaseSelect = $("<select class='score-control-phase'></select>");
    if (needsPhase) {
      element.phases.forEach(function (phaseId) {
        const phase = GameView.phase(phaseId);
        phaseSelect.append($("<option></option>").attr("value", phaseId).text(phase ? phase.name : phaseId));
      });
      phaseSelect.val(element.phases[element.phases.length - 1]);
      node.append($("<label class='score-control-phase-label'>Phase </label>").append(phaseSelect));
    }
    let state = {raw: undefined, enabled: false, clock: {state: "PRE_MATCH"}};
    let phaseChosen = false;
    phaseSelect.on("change", function () {
      phaseChosen = true;
      render();
    });

    const send = function (op, value) {
      const command = {alliance: options.allianceId, robot: options.robot || 0, element: element.id, op: op};
      if (value !== undefined) {
        command.value = value;
      }
      if (needsPhase && state.clock.state === "POST_MATCH") {
        command.bucket = phaseSelect.val();
      }
      options.send(command);
    };

    let render;
    switch (element.type) {
      case "counter": {
        const minus = $("<button type='button' class='score-button score-stepper-minus'>&minus;</button>");
        const readout = $("<div class='score-readout'></div>");
        const plus = $("<button type='button' class='score-button score-stepper-plus'>+</button>");
        body.append(minus, readout, plus);
        minus.on("click", function () { send("decrement"); });
        plus.on("click", function () { send("increment"); });
        render = function () {
          const counts = (state.raw || {}).counts || {};
          const total = GameView.counterTotal(state.raw);
          const postMatch = state.clock.state === "POST_MATCH";
          const inPhase = counts[phaseSelect.val()] || 0;
          readout.text(postMatch ? inPhase + " of " + total : String(total));
          // A count cannot go below zero in the phase a decrement is credited to. After the match that is the phase
          // picked here; during it the server decides, and says so if there is nothing left to take away.
          minus.prop("disabled", !state.enabled || (postMatch ? inPhase <= 0 : total <= 0));
          plus.prop("disabled", !state.enabled || (element.max !== undefined && total >= element.max));
        };
        break;
      }
      case "boolean": {
        const button = $("<button type='button' class='score-button score-toggle'></button>").appendTo(body);
        button.on("click", function () { send("set", !((state.raw || {}).bool === true)); });
        render = function () {
          const on = (state.raw || {}).bool === true;
          button.text(on ? "Yes" : "No").attr("data-selected", on).prop("disabled", !state.enabled);
        };
        break;
      }
      case "enum": {
        const selected = function () {
          return GameView.plainValue(element, state.raw);
        };
        if (element.widget === "select") {
          const select = $("<select class='score-select'></select>").appendTo(body);
          element.options.forEach(function (option) {
            select.append($("<option></option>").attr("value", option.id).text(option.name));
          });
          select.on("change", function () { send("set", select.val()); });
          render = function () {
            select.val(selected()).prop("disabled", !state.enabled);
          };
        } else {
          const buttons = element.options.map(function (option) {
            const button = $("<button type='button' class='score-button score-option'></button>")
              .text(option.name).appendTo(body);
            button.on("click", function () { send("set", option.id); });
            return {id: option.id, button: button};
          });
          render = function () {
            const current = selected();
            buttons.forEach(function (entry) {
              entry.button.attr("data-selected", entry.id === current).prop("disabled", !state.enabled);
            });
          };
        }
        break;
      }
      case "timer": {
        const readout = $("<div class='score-readout'></div>");
        const startStop = $("<button type='button' class='score-button score-timer-button'></button>");
        const secondsInput = $("<input type='number' class='score-input' min='0' step='0.1'>");
        body.append(readout, startStop, secondsInput);
        startStop.on("click", function () {
          send((state.raw || {}).running === true ? "timer_stop" : "timer_start");
        });
        secondsInput.on("change", function () {
          const seconds = parseFloat(secondsInput.val());
          if (!isNaN(seconds) && seconds >= 0) {
            send("set", seconds);
          }
        });
        render = function () {
          const raw = state.raw || {};
          const postMatch = state.clock.state === "POST_MATCH";
          let seconds = raw.seconds || 0;
          if (raw.running === true && state.clock.matchTimeSec !== undefined) {
            seconds += Math.max(0, state.clock.matchTimeSec - (raw.since || 0));
          }
          readout.text(ScoreControls.formatSeconds(seconds));
          node.attr("data-running", raw.running === true);
          // The clock has stopped once the match is over, so the time is then typed in instead.
          startStop.text(raw.running === true ? "Stop" : "Start").prop("disabled", !state.enabled).toggle(!postMatch);
          secondsInput.prop("disabled", !state.enabled).toggle(postMatch);
          if (!secondsInput.is(":focus")) {
            secondsInput.val(Math.round((raw.seconds || 0) * 10) / 10);
          }
        };
        break;
      }
      default: {
        const step = element.step !== undefined ? element.step : 1;
        const input = $("<input class='score-input'>").attr("type", element.widget === "slider" ? "range" : "number")
          .attr("step", step);
        if (element.min !== undefined) {
          input.attr("min", element.min);
        }
        if (element.max !== undefined) {
          input.attr("max", element.max);
        }
        const readout = $("<div class='score-readout'></div>");
        body.append(input, readout);
        input.on("change", function () {
          const value = parseFloat(input.val());
          if (!isNaN(value)) {
            send("set", value);
          }
        });
        render = function () {
          const value = (state.raw || {}).number || 0;
          readout.text(GameView.formatNumber(value) + (element.unit ? " " + element.unit : ""));
          input.prop("disabled", !state.enabled);
          if (!input.is(":focus")) {
            input.val(value);
          }
        };
      }
    }

    return {
      node: node,
      update: function (raw, enabled, clock) {
        state = {raw: raw, enabled: enabled, clock: clock};
        const postMatch = clock.state === "POST_MATCH";
        node.find(".score-control-phase-label").toggle(needsPhase && postMatch);
        // Until the person picks one, a boolean or an enum offers the phase its value was set in.
        if (needsPhase && !phaseChosen && raw && raw.phase && element.phases.includes(raw.phase)) {
          phaseSelect.val(raw.phase);
        }
        phaseSelect.prop("disabled", !enabled);
        node.attr("data-enabled", enabled);
        render();
      },
    };
  },

  // Builds the controls of one section of a panel (see panelSections) into a container and returns them as a list
  // of {element, allianceId, robot, control}. teamAt(allianceId, position) returns the team number to label a
  // robot's control with, or 0 if there is none.
  buildSection: function (container, section, teamAt, send) {
    const view = GameView.view;
    const controls = [];
    const alliance = GameView.alliance(section.allianceId);
    const sectionNode = $("<section class='score-section'></section>").appendTo(container);
    if (alliance !== null) {
      sectionNode.attr("data-alliance-index", GameView.allianceIndex(alliance.id));
    }
    sectionNode.append($("<h2 class='score-section-title'></h2>").text(alliance === null ? "Field" : alliance.name));

    const groups = [];
    section.elements.forEach(function (element) {
      if (!groups.includes(element.group)) {
        groups.push(element.group);
      }
    });
    groups.forEach(function (group) {
      const groupNode = $("<div class='score-group'></div>").appendTo(sectionNode);
      groupNode.append($("<h3 class='score-group-title'></h3>").text(group));
      section.elements.filter(function (element) { return element.group === group; }).forEach(function (element) {
        const elementNode = $("<div class='score-element'></div>").attr("data-scope", element.scope)
          .appendTo(groupNode);
        elementNode.append($("<div class='score-element-name'></div>").text(element.name));
        const row = $("<div class='score-element-controls'></div>").appendTo(elementNode);
        const positions = element.scope === "robot" ? view.allianceSize : 1;
        for (let position = 1; position <= positions; position++) {
          const robot = element.scope === "robot" ? position : 0;
          let label = "";
          if (robot > 0) {
            const team = teamAt(section.allianceId, robot);
            label = team ? String(team) : "Robot " + robot;
          }
          const control = ScoreControls.create(
            {element: element, allianceId: section.allianceId, robot: robot, label: label, send: send}
          );
          row.append(control.node);
          controls.push({element: element, allianceId: section.allianceId, robot: robot, control: control});
        }
      });
    });
    return controls;
  },

  // Describes a score change (a scoring.Change as sent in the "scoreChange" message) in a few words.
  describeChange: function (change, teamAt) {
    const parts = [];
    const alliance = GameView.alliance(change.alliance || "");
    if (alliance !== null) {
      parts.push(alliance.name);
    }
    if (change.robot > 0) {
      const team = teamAt(change.alliance, change.robot);
      parts.push(team ? String(team) : "Robot " + change.robot);
    }
    const element = GameView.element(change.element);
    const name = element === null ? change.element : element.name;
    let value = change.value;
    if (element !== null && element.type === "enum") {
      value = GameView.optionName(element, value);
    } else if (value === true) {
      value = "yes";
    } else if (value === false) {
      value = "no";
    }
    switch (change.op) {
      case "increment":
        parts.push(name + " +" + (value === undefined || value === null ? 1 : value));
        break;
      case "decrement":
        parts.push(name + " −" + (value === undefined || value === null ? 1 : value));
        break;
      case "set":
        parts.push(name + " = " + value);
        break;
      case "toggle":
        parts.push(name + " toggled");
        break;
      case "timer_start":
        parts.push(name + " started");
        break;
      case "timer_stop":
        parts.push(name + " stopped");
        break;
      default:
        parts.push(name + " " + change.op);
    }
    const phase = GameView.phase(change.bucket || change.phase);
    if (phase !== null) {
      parts.push(phase.name);
    }
    return parts.join(" · ");
  },
};
