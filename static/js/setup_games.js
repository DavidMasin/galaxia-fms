// Client-side logic for the game editor: the list of games, the YAML editor with live validation, and the sandbox
// in which a test match can be scored by clicking.

var currentId = null;       // Id of the game being edited.
var savedYaml = "";         // The YAML as last loaded or saved.
var currentView = null;     // Browser view of the game, or null while the definition has errors.
var sandboxState = null;    // What has been scored in the sandbox.
var validateTimer = null;
var scoreRequestNumber = 0;

const newGameTemplate = function (id) {
  const name = id.split("-").map(function (word) { return word.charAt(0).toUpperCase() + word.slice(1); }).join(" ");
  return [
    "schema: 1",
    "id: " + id,
    "name: " + name,
    "description: Describe the game here.",
    "",
    "alliances:",
    "  size: 3",
    "  list:",
    "    - { id: red, name: Red, short: R, color: \"#d32f2f\" }",
    "    - { id: blue, name: Blue, short: B, color: \"#1565c0\" }",
    "",
    "match:",
    "  countdown: { enabled: true, voice: drivers_behind_lines, beats: 3 }",
    "  phases:",
    "    - { id: auto, name: Autonomous, duration: 15, robots: auto }",
    "    - { id: pause, name: Pause, duration: 2, robots: disabled }",
    "    - { id: teleop, name: Teleop, duration: 120, robots: teleop }",
    "  score_grace: 2",
    "",
    "elements:",
    "  - id: goals",
    "    name: Goals",
    "    type: counter",
    "    scope: alliance",
    "    points: { auto: 2, teleop: 1 }",
    "",
    "ranking:",
    "  points:",
    "    - { id: win, name: Win, expr: \"result == 'win' ? 2 : result == 'tie' ? 1 : 0\" }",
    "",
  ].join("\n");
};

const escapeHtml = function (text) {
  return $("<div>").text(text === null || text === undefined ? "" : String(text)).html();
};

const formatNumber = function (value) {
  if (typeof value !== "number") {
    return escapeHtml(value);
  }
  return String(Math.round(value * 100) / 100);
};

// ---------------------------------------------------------------------------------------------------------------
// Game list

const loadGameList = function (selectId) {
  $.getJSON("/api/v1/games", function (data) {
    const list = $("#gameList").empty();
    data.games.forEach(function (game) {
      const item = $("<a href='#' class='list-group-item list-group-item-action'>").attr("data-id", game.id);
      item.append($("<div class='fw-bold'>").text(game.name));
      item.append($("<small class='text-secondary'>").text(game.id));
      if (!game.valid) {
        item.append(" ").append($("<span class='badge text-bg-danger'>").text("errors"));
      }
      item.toggleClass("active", game.id === currentId);
      item.on("click", function (event) {
        event.preventDefault();
        selectGame(game.id);
      });
      list.append(item);
    });
    if (selectId) {
      selectGame(selectId);
    } else if (currentId === null && data.games.length > 0) {
      selectGame(data.games[0].id);
    }
  });
};

const isDirty = function () {
  return currentId !== null && $("#yaml").val() !== savedYaml;
};

const selectGame = function (id) {
  if (isDirty() && !confirm("Discard the unsaved changes to " + currentId + "?")) {
    return;
  }
  $.getJSON("/api/v1/games/" + encodeURIComponent(id), function (data) {
    currentId = id;
    savedYaml = data.yaml;
    $("#yaml").val(data.yaml);
    sandboxState = null;
    showValidation(data);
    updateGutter();
    $("#gameList a").removeClass("active").filter("[data-id='" + id + "']").addClass("active");
  });
};

// ---------------------------------------------------------------------------------------------------------------
// Editor

const updateGutter = function (errorLines) {
  const lineCount = $("#yaml").val().split("\n").length;
  const errors = errorLines || $("#gutter").data("errors") || {};
  $("#gutter").data("errors", errors);
  let html = "";
  for (let line = 1; line <= lineCount; line++) {
    html += errors[line] ? "<span class='has-error'>" + line + "</span>\n" : line + "\n";
  }
  $("#gutter").html(html).scrollTop($("#yaml").scrollTop());
};

const showValidation = function (data) {
  currentView = data.valid ? data.view : null;
  const dirty = isDirty();
  $("#gameTitle").text(data.valid ? data.view.name : (currentId || "New game"));
  $("#gameStatus").removeClass("text-bg-success text-bg-danger text-bg-warning")
    .addClass(data.valid ? (dirty ? "text-bg-warning" : "text-bg-success") : "text-bg-danger")
    .text(data.valid ? (dirty ? "Valid, not saved" : "Valid") : "Has errors");
  $("#saveGame").prop("disabled", !data.valid || !dirty);
  $("#revertGame").prop("disabled", !dirty);

  const errorLines = {};
  const container = $("#diagnostics").empty();
  data.diagnostics.forEach(function (diagnostic) {
    if (diagnostic.line > 0) {
      errorLines[diagnostic.line] = true;
    }
    const where = diagnostic.line > 0 ? "Line " + diagnostic.line : (diagnostic.path || "File");
    const item = $("<div class='alert py-2 mb-2 diagnostic'>")
      .addClass(diagnostic.severity === "error" ? "alert-danger" : "alert-warning")
      .append($("<strong>").text(where + ": ")).append(document.createTextNode(diagnostic.message));
    item.on("click", function () { goToLine(diagnostic.line); });
    container.append(item);
  });
  if (data.valid && data.diagnostics.length === 0) {
    const view = data.view;
    container.append($("<div class='text-secondary small'>").text(
      view.alliances.length + " alliances of " + view.allianceSize + ", " + view.phases.length + " phases (" +
      Math.floor(view.duration / 60) + ":" + String(Math.floor(view.duration % 60)).padStart(2, "0") + "), " +
      view.elements.length + " scoring elements, " + view.rankingPoints.length + " ranking points."
    ));
  }
  updateGutter(errorLines);
  rebuildSandbox();
};

const goToLine = function (line) {
  if (!line) {
    return;
  }
  const textarea = $("#yaml")[0];
  const lines = textarea.value.split("\n");
  let start = 0;
  for (let i = 0; i < line - 1 && i < lines.length; i++) {
    start += lines[i].length + 1;
  }
  textarea.focus();
  textarea.setSelectionRange(start, start + (lines[line - 1] || "").length);
  const lineHeight = textarea.scrollHeight / lines.length;
  textarea.scrollTop = Math.max(0, (line - 4) * lineHeight);
};

const validateNow = function () {
  $.ajax({
    url: "/api/v1/games/validate", method: "POST", contentType: "text/plain", data: $("#yaml").val(),
    success: showValidation,
  });
};

const saveGame = function () {
  $.ajax({
    url: "/api/v1/games/" + encodeURIComponent(currentId), method: "PUT", contentType: "text/plain",
    data: $("#yaml").val(),
    success: function (data) {
      savedYaml = data.yaml;
      showValidation(data);
      loadGameList();
    },
    error: function (xhr) {
      if (xhr.responseJSON && xhr.responseJSON.diagnostics) {
        showValidation(xhr.responseJSON);
      } else {
        alert("Could not save: " + (xhr.responseJSON ? xhr.responseJSON.error : xhr.statusText));
      }
    },
  });
};

const newGame = function () {
  const id = prompt("Id for the new game (lowercase letters, digits and dashes):", "my-game");
  if (!id) {
    return;
  }
  if (!/^[a-z][a-z0-9-]{0,39}$/.test(id)) {
    alert("Use lowercase letters, digits and dashes, starting with a letter.");
    return;
  }
  if (isDirty() && !confirm("Discard the unsaved changes to " + currentId + "?")) {
    return;
  }
  currentId = id;
  savedYaml = "";
  sandboxState = null;
  $("#gameList a").removeClass("active");
  $("#yaml").val(newGameTemplate(id));
  validateNow();
};

// ---------------------------------------------------------------------------------------------------------------
// Sandbox

const emptySandboxState = function (view) {
  const state = {alliances: {}, field: {}, params: {}, playoff: false};
  view.alliances.forEach(function (alliance) {
    const robots = [];
    for (let i = 0; i < view.allianceSize; i++) {
      robots.push({});
    }
    state.alliances[alliance.id] = {elements: {}, robots: robots, fouls: {}, cards: {}, disqualified: false};
  });
  return state;
};

// Returns the place in the sandbox state where an element's value for one owner is kept.
const valueHolder = function (element, allianceId, robotIndex) {
  if (element.scope === "field") {
    return sandboxState.field;
  }
  const alliance = sandboxState.alliances[allianceId];
  return element.scope === "robot" ? alliance.robots[robotIndex] : alliance.elements;
};

const phaseName = function (phaseId) {
  const phase = currentView.phases.find(function (p) { return p.id === phaseId; });
  return phase ? phase.name : phaseId;
};

// Builds the control for one element and one owner.
const buildControl = function (element, allianceId, robotIndex) {
  const holder = valueHolder(element, allianceId, robotIndex);
  const row = $("<div class='sandbox-control'>").append($("<label>").text(element.name));
  const changed = function () { scoreSandbox(); };

  const phaseSelect = function (value) {
    const select = $("<select class='form-select form-select-sm' style='width: auto;'>");
    element.phases.forEach(function (phaseId) {
      select.append($("<option>").val(phaseId).text(phaseName(phaseId)));
    });
    select.val(value.phase || element.phases[0]);
    select.on("change", function () { value.phase = select.val(); changed(); });
    value.phase = select.val();
    return select;
  };

  if (element.type === "counter") {
    const counts = holder[element.id] = holder[element.id] || {};
    element.phases.forEach(function (phaseId) {
      const count = $("<span class='count'>").text(counts[phaseId] || 0);
      const step = function (delta) {
        counts[phaseId] = Math.max(0, (counts[phaseId] || 0) + delta);
        count.text(counts[phaseId]);
        changed();
      };
      const stepper = $("<span class='sandbox-stepper'>");
      if (element.phases.length > 1) {
        stepper.append($("<span class='phase-label'>").text(phaseName(phaseId)));
      }
      stepper.append($("<button type='button' class='btn btn-outline-secondary btn-sm'>").text("−")
        .on("click", function () { step(-1); }));
      stepper.append(count);
      stepper.append($("<button type='button' class='btn btn-outline-secondary btn-sm'>").text("+")
        .on("click", function (event) { step(event.shiftKey ? 5 : 1); }));
      row.append(stepper);
    });
  } else if (element.type === "boolean") {
    const value = holder[element.id] = holder[element.id] || {value: element.initial === true};
    const box = $("<input type='checkbox' class='form-check-input'>").prop("checked", value.value);
    box.on("change", function () { value.value = box.prop("checked"); changed(); });
    row.append(box);
    if (element.phasePoints && element.phases.length > 1) {
      row.append(phaseSelect(value));
    } else {
      value.phase = element.phases[0];
    }
  } else if (element.type === "enum") {
    const value = holder[element.id] = holder[element.id] || {value: element.initial || element.options[0].id};
    const select = $("<select class='form-select form-select-sm' style='width: auto;'>");
    element.options.forEach(function (option) {
      select.append($("<option>").val(option.id).text(option.name));
    });
    select.val(value.value);
    select.on("change", function () { value.value = select.val(); changed(); });
    row.append(select);
    if (element.phasePoints && element.phases.length > 1) {
      row.append(phaseSelect(value));
    } else {
      value.phase = element.phases[0];
    }
  } else {
    // Timers are entered as seconds, numbers as themselves.
    const initial = typeof element.initial === "number" ? element.initial : (element.min > 0 ? element.min : 0);
    if (holder[element.id] === undefined) {
      holder[element.id] = initial;
    }
    const input = $("<input type='number' class='form-control form-control-sm'>").val(holder[element.id]);
    if (element.min !== undefined) { input.attr("min", element.min); }
    if (element.max !== undefined) { input.attr("max", element.max); }
    input.attr("step", element.step || (element.type === "timer" ? 0.1 : "any"));
    input.on("input", function () {
      const parsed = parseFloat(input.val());
      holder[element.id] = isNaN(parsed) ? initial : parsed;
      changed();
    });
    row.append(input);
    row.append($("<span class='phase-label'>").text(element.type === "timer" ? "seconds" : (element.unit || "")));
  }
  return row;
};

const sortedElements = function (scope) {
  return currentView.elements.filter(function (element) { return element.scope === scope; })
    .sort(function (a, b) { return a.order - b.order; });
};

const rebuildSandbox = function () {
  $("#sandboxUnavailable").toggle(currentView === null);
  $("#sandbox").toggle(currentView !== null);
  if (currentView === null) {
    return;
  }
  const view = currentView;
  const previous = sandboxState;
  sandboxState = emptySandboxState(view);
  if (previous) {
    // Keep what was scored when the definition changes, as far as it still fits. Anything that no longer fits is
    // reported by the server and can be cleared with Reset.
    sandboxState.params = previous.params;
    sandboxState.playoff = previous.playoff;
    view.alliances.forEach(function (alliance) {
      const old = previous.alliances[alliance.id];
      if (old && old.robots.length === view.allianceSize) {
        sandboxState.alliances[alliance.id] = old;
      }
    });
    sandboxState.field = previous.field;
    const known = {};
    view.elements.forEach(function (element) { known[element.scope + ":" + element.id] = element.type; });
    const prune = function (holder, scope) {
      Object.keys(holder).forEach(function (id) {
        if (!known[scope + ":" + id]) { delete holder[id]; }
      });
    };
    prune(sandboxState.field, "field");
    Object.values(sandboxState.alliances).forEach(function (alliance) {
      prune(alliance.elements, "alliance");
      alliance.robots.forEach(function (robot) { prune(robot, "robot"); });
    });
  }

  // Clock.
  const phaseSelect = $("#sandboxPhase").empty().append($("<option>").val("").text("Not running"));
  view.phases.forEach(function (phase) {
    phaseSelect.append($("<option>").val(phase.id).text(phase.name));
  });
  $("#sandboxPlayoff").prop("checked", sandboxState.playoff);

  // Parameters.
  const params = $("#sandboxParams").empty();
  if (view.params.length > 0) {
    const card = $("<div class='card card-body'>").append($("<h6>").text("Event parameters"));
    view.params.forEach(function (param) {
      const row = $("<div class='sandbox-control'>").append($("<label style='min-width: 18rem;'>").text(param.name));
      if (param.type === "bool") {
        const current = sandboxState.params[param.id] === undefined ? param.default : sandboxState.params[param.id] !== 0;
        const box = $("<input type='checkbox' class='form-check-input'>").prop("checked", current);
        box.on("change", function () { sandboxState.params[param.id] = box.prop("checked") ? 1 : 0; scoreSandbox(); });
        row.append(box);
      } else {
        const current = sandboxState.params[param.id] === undefined ? param.default : sandboxState.params[param.id];
        const input = $("<input type='number' class='form-control form-control-sm'>").val(current);
        input.attr("step", param.type === "int" ? 1 : "any");
        input.on("input", function () {
          const parsed = parseFloat(input.val());
          if (isNaN(parsed)) { delete sandboxState.params[param.id]; } else { sandboxState.params[param.id] = parsed; }
          scoreSandbox();
        });
        row.append(input);
      }
      card.append(row);
    });
    params.append(card);
  }

  // One column per alliance.
  const columns = $("#sandboxAlliances").empty();
  const columnClass = view.alliances.length >= 3 ? "col-xl-4 col-lg-6" : "col-lg-6";
  view.alliances.forEach(function (alliance) {
    const allianceState = sandboxState.alliances[alliance.id];
    const card = $("<div class='card card-body sandbox-alliance h-100'>").css("border-top-color", alliance.color);
    card.append($("<h5>").text(alliance.name));
    sortedElements("alliance").forEach(function (element) {
      card.append(buildControl(element, alliance.id, 0));
    });
    const robotElements = sortedElements("robot");
    if (robotElements.length > 0) {
      for (let i = 0; i < view.allianceSize; i++) {
        card.append($("<div class='text-secondary small mt-2'>").text("Robot " + alliance.short + (i + 1)));
        robotElements.forEach(function (element) {
          card.append(buildControl(element, alliance.id, i));
        });
      }
    }
    if (view.foulTypes.length > 0) {
      card.append($("<div class='text-secondary small mt-2'>").text("Fouls against " + alliance.name));
      view.foulTypes.forEach(function (foulType) {
        const count = $("<span class='count'>").text(allianceState.fouls[foulType.id] || 0);
        const step = function (delta) {
          allianceState.fouls[foulType.id] = Math.max(0, (allianceState.fouls[foulType.id] || 0) + delta);
          count.text(allianceState.fouls[foulType.id]);
          scoreSandbox();
        };
        card.append($("<div class='sandbox-control'>").append($("<label>").text(foulType.name))
          .append($("<span class='sandbox-stepper'>")
            .append($("<button type='button' class='btn btn-outline-secondary btn-sm'>").text("−")
              .on("click", function () { step(-1); }))
            .append(count)
            .append($("<button type='button' class='btn btn-outline-secondary btn-sm'>").text("+")
              .on("click", function () { step(1); }))));
      });
    }
    if (view.cards.length > 0) {
      for (let i = 0; i < view.allianceSize; i++) {
        const select = $("<select class='form-select form-select-sm' style='width: auto;'>")
          .append($("<option>").val("").text("No card"));
        view.cards.forEach(function (cardType) {
          select.append($("<option>").val(cardType.id).text(cardType.name));
        });
        select.val(allianceState.cards[i + 1] || "");
        select.on("change", function () {
          if (select.val()) { allianceState.cards[i + 1] = select.val(); } else { delete allianceState.cards[i + 1]; }
          scoreSandbox();
        });
        card.append($("<div class='sandbox-control'>").append($("<label>").text("Card " + alliance.short + (i + 1)))
          .append(select));
      }
    }
    const disqualified = $("<input type='checkbox' class='form-check-input'>").prop("checked", allianceState.disqualified);
    disqualified.on("change", function () { allianceState.disqualified = disqualified.prop("checked"); scoreSandbox(); });
    card.append($("<div class='sandbox-control'>").append($("<label>").text("Disqualified")).append(disqualified));
    columns.append($("<div>").addClass(columnClass).append(card));
  });

  // Field elements.
  const field = $("#sandboxField").empty();
  const fieldElements = sortedElements("field");
  if (fieldElements.length > 0) {
    const card = $("<div class='card card-body'>").append($("<h6>").text("Field"));
    fieldElements.forEach(function (element) {
      card.append(buildControl(element, null, 0));
    });
    field.append(card);
  }
  scoreSandbox();
};

// Converts the sandbox state into the compact form the scoring API reads.
const compactState = function () {
  const compactValue = function (element, raw) {
    if (element.type === "counter") {
      const counts = {};
      Object.keys(raw).forEach(function (phaseId) {
        if (raw[phaseId] > 0) { counts[phaseId] = raw[phaseId]; }
      });
      return Object.keys(counts).length > 0 ? counts : undefined;
    }
    if (element.type === "boolean" || element.type === "enum") {
      return {value: raw.value, phase: raw.phase};
    }
    return raw;
  };
  const collect = function (holder, scope) {
    const out = {};
    currentView.elements.forEach(function (element) {
      if (element.scope === scope && holder[element.id] !== undefined) {
        const value = compactValue(element, holder[element.id]);
        if (value !== undefined) { out[element.id] = value; }
      }
    });
    return out;
  };
  const state = {field: collect(sandboxState.field, "field")};
  currentView.alliances.forEach(function (alliance) {
    const source = sandboxState.alliances[alliance.id];
    const compact = collect(source.elements, "alliance");
    compact.robots = source.robots.map(function (robot) { return collect(robot, "robot"); });
    compact.fouls = [];
    Object.keys(source.fouls).forEach(function (type) {
      for (let i = 0; i < source.fouls[type]; i++) { compact.fouls.push(type); }
    });
    compact.cards = source.cards;
    compact.disqualified = source.disqualified;
    state[alliance.id] = compact;
  });
  return state;
};

const scoreSandbox = function () {
  if (currentView === null || sandboxState === null) {
    return;
  }
  sandboxState.playoff = $("#sandboxPlayoff").prop("checked");
  const requestNumber = ++scoreRequestNumber;
  $.ajax({
    url: "/api/v1/games/score", method: "POST", contentType: "application/json",
    data: JSON.stringify({
      yaml: $("#yaml").val(), state: compactState(), params: sandboxState.params, playoff: sandboxState.playoff,
      clock: {phase: $("#sandboxPhase").val(), elapsed: parseFloat($("#sandboxElapsed").val()) || 0},
    }),
    success: function (data) {
      if (requestNumber === scoreRequestNumber) {
        $("#sandboxError").hide();
        renderResults(data);
      }
    },
    error: function (xhr) {
      if (requestNumber === scoreRequestNumber) {
        $("#sandboxError").text(xhr.responseJSON ? xhr.responseJSON.error : xhr.statusText).show();
      }
    },
  });
};

const renderResults = function (data) {
  const view = currentView;
  const result = data.result;
  const allianceCells = function (render) {
    return view.alliances.map(function (alliance) {
      return "<td>" + render(result.alliances[alliance.id], alliance) + "</td>";
    }).join("");
  };

  $("#resultsHead").html("<tr><th></th>" + view.alliances.map(function (alliance) {
    return "<th style='color: " + alliance.color + "'>" + escapeHtml(alliance.name) + "</th>";
  }).join("") + "</tr>");

  let rows = "";
  view.breakdown.forEach(function (row, index) {
    rows += "<tr><td>" + escapeHtml(row.name) + "</td>" +
      allianceCells(function (a) { return formatNumber(a.breakdown[index].value); }) + "</tr>";
  });
  rows += "<tr class='score-row'><td>Score</td>" + allianceCells(function (a) { return formatNumber(a.score); }) + "</tr>";
  rows += "<tr><td>Result</td>" + allianceCells(function (a) { return escapeHtml(a.result); }) + "</tr>";
  view.rankingPoints.forEach(function (rankingPoint) {
    rows += "<tr><td>RP: " + escapeHtml(rankingPoint.name) + "</td>" +
      allianceCells(function (a) { return formatNumber(a.rp[rankingPoint.id]); }) + "</tr>";
  });
  if (view.rankingPoints.length > 0) {
    rows += "<tr class='fw-bold'><td>Ranking points</td>" +
      allianceCells(function (a) { return formatNumber(a.rp_total); }) + "</tr>";
  }
  $("#resultsBody").html(rows);

  let winner = "Tie";
  if (result.winner) {
    const alliance = view.alliances.find(function (a) { return a.id === result.winner; });
    winner = alliance.name + " wins" + (result.tiebreak ? " on the tiebreaker: " + result.tiebreak : "");
  }
  $("#sandboxWinner").text(winner);

  // Supporting detail: points by source, derived values, live values.
  const extras = $("#sandboxExtras").empty();
  const table = function (title, names, cell) {
    if (names.length === 0) {
      return;
    }
    let html = "<table class='table table-sm sandbox-results'><thead><tr><th>" + escapeHtml(title) + "</th>" +
      view.alliances.map(function (alliance) { return "<th>" + escapeHtml(alliance.name) + "</th>"; }).join("") +
      "</tr></thead><tbody>";
    names.forEach(function (item) {
      html += "<tr><td>" + escapeHtml(item.name) + "</td>" + view.alliances.map(function (alliance) {
        return "<td>" + formatNumber(cell(alliance.id, item)) + "</td>";
      }).join("") + "</tr>";
    });
    extras.append($("<div class='col-lg-6'>").html(html + "</tbody></table>"));
  };
  const pointSources = view.elements.filter(function (e) { return e.scope !== "field"; })
    .concat(view.derived.filter(function (d) { return d.points; }))
    .concat([{id: "__fouls", name: "Fouls by other alliances"}]);
  table("Points from", pointSources, function (allianceId, item) {
    const alliance = result.alliances[allianceId];
    return item.id === "__fouls" ? alliance.foul_points : alliance.points[item.id];
  });
  table("Values", view.derived.filter(function (d) { return !d.points && d.scope === "alliance"; }),
    function (allianceId, item) { return result.alliances[allianceId].values[item.id]; });
  table("Live values", view.live.filter(function (l) { return l.scope === "alliance"; }),
    function (allianceId, item) { return data.live.alliances[allianceId][item.id]; });
  table("Ranking stats", view.rankingStats,
    function (allianceId, item) { return result.alliances[allianceId].stats[item.id]; });

  const fieldValues = view.derived.filter(function (d) { return d.scope === "field"; })
    .map(function (d) { return {name: d.name, value: result.field[d.id]}; })
    .concat(view.live.filter(function (l) { return l.scope === "field"; })
      .map(function (l) { return {name: l.name + " (live)", value: data.live.field[l.id]}; }));
  if (fieldValues.length > 0) {
    let html = "<table class='table table-sm sandbox-results'><thead><tr><th>Field</th><th></th></tr></thead><tbody>";
    fieldValues.forEach(function (item) {
      html += "<tr><td>" + escapeHtml(item.name) + "</td><td>" + formatNumber(item.value) + "</td></tr>";
    });
    extras.append($("<div class='col-lg-6'>").html(html + "</tbody></table>"));
  }
};

// ---------------------------------------------------------------------------------------------------------------

$(function () {
  $("#yaml").on("input", function () {
    updateGutter();
    $("#saveGame").prop("disabled", true);
    clearTimeout(validateTimer);
    validateTimer = setTimeout(validateNow, 400);
  }).on("scroll", function () {
    $("#gutter").scrollTop($(this).scrollTop());
  }).on("keydown", function (event) {
    // Tab inserts two spaces, as YAML wants, instead of leaving the editor.
    if (event.key === "Tab") {
      event.preventDefault();
      const start = this.selectionStart;
      this.value = this.value.substring(0, start) + "  " + this.value.substring(this.selectionEnd);
      this.selectionStart = this.selectionEnd = start + 2;
      $(this).trigger("input");
    }
  });
  $("#saveGame").on("click", saveGame);
  $("#revertGame").on("click", function () {
    $("#yaml").val(savedYaml);
    updateGutter();
    validateNow();
  });
  $("#newGame").on("click", newGame);
  $("#sandboxReset").on("click", function () {
    sandboxState = null;
    rebuildSandbox();
  });
  $("#sandboxPlayoff, #sandboxPhase").on("change", scoreSandbox);
  $("#sandboxElapsed").on("input", scoreSandbox);
  $(".nav-tabs a").on("click", function (event) {
    event.preventDefault();
    $(".nav-tabs a").removeClass("active");
    $(this).addClass("active");
    $("#editorTab, #sandboxTab").hide();
    $("#" + $(this).data("tab")).show();
  });
  $(window).on("beforeunload", function () {
    if (isDirty()) {
      return "There are unsaved changes.";
    }
  });
  loadGameList();
});
