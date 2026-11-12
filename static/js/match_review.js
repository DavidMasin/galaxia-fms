// Copyright 2014 Team 254. All Rights Reserved.
// Author: pat@patfairbank.com (Patrick Fairbank)
//
// Client-side methods for editing a match in the match review page.
//
// The editor is generated from the game the result was played with, which the page embeds together with the raw
// score state of the result. It edits a copy of that raw state: counts per phase, flags, selections, times, numbers,
// fouls, cards and the disqualification flag. The server turns the difference into audited score changes when the
// form is saved, and computes the summary shown here from the same state each time something changes.

let editData;
let editedState;
const SUMMARY_REFRESH_DELAY_MS = 150;
let summaryRefreshTimer;
let latestSummaryRequestId = 0;

// Returns a fresh form control with Bootstrap's styling.
const newInput = function (type) {
  return $("<input>").attr("type", type).addClass(type === "checkbox" ? "form-check-input" : "form-control form-control-sm");
};

const newSelect = function (choices, selected) {
  const select = $("<select class='form-select form-select-sm'></select>");
  choices.forEach(function (choice) {
    select.append($("<option></option>").attr("value", choice.value).text(choice.text));
  });
  select.val(selected);
  return select;
};

// Returns the choices for the phase a value is credited to: the phases the element accepts.
const phaseChoices = function (element) {
  return element.phases.map(function (phaseId) {
    const phase = GameView.phase(phaseId);
    return {value: phaseId, text: phase === null ? phaseId : phase.name};
  });
};

// Builds the control that edits one raw value in place. raw is the object inside editedState that holds the value;
// it is changed directly, and changed() is called afterwards.
const buildValueEditor = function (element, raw, changed) {
  const node = $("<div class='result-editor-value'></div>");
  switch (element.type) {
    case "counter": {
      // A counter is kept per phase. Every phase the element accepts is offered, and any other one that holds a
      // count, so that nothing stored is hidden.
      raw.counts = raw.counts || {};
      const phases = GameView.phases().filter(function (phase) {
        return element.phases.includes(phase.id) || raw.counts[phase.id];
      });
      const total = $("<span class='badge text-bg-secondary'></span>");
      const showTotal = function () {
        total.text("Total " + GameView.counterTotal(raw));
      };
      const row = $("<div class='result-editor-phases'></div>").appendTo(node);
      phases.forEach(function (phase) {
        const input = newInput("number").attr("min", 0).attr("step", 1).val(raw.counts[phase.id] || 0);
        input.on("input change", function () {
          const count = parseInt(input.val(), 10);
          if (isNaN(count) || count === 0) {
            delete raw.counts[phase.id];
          } else {
            raw.counts[phase.id] = count;
          }
          showTotal();
          changed();
        });
        row.append($("<label></label>").append($("<span></span>").text(phase.name), input));
      });
      row.append(total);
      showTotal();
      break;
    }
    case "boolean":
    case "enum": {
      const row = $("<div class='result-editor-phases'></div>").appendTo(node);
      // The phase a value was set in only matters when the points depend on it.
      const phaseSelect = newSelect(phaseChoices(element), element.phases.includes(raw.phase) ? raw.phase :
        element.phases[element.phases.length - 1]);
      const update = function () {
        if (element.type === "boolean") {
          raw.bool = valueInput.prop("checked");
        } else {
          raw.option = valueInput.val();
        }
        if (element.phasePoints === true) {
          raw.phase = phaseSelect.val();
        }
        changed();
      };
      let valueInput;
      if (element.type === "boolean") {
        valueInput = newInput("checkbox").prop("checked", raw.bool === true);
      } else {
        valueInput = newSelect(element.options.map(function (option) {
          return {value: option.id, text: option.name};
        }), GameView.plainValue(element, raw));
      }
      valueInput.on("change", update);
      phaseSelect.on("change", update);
      row.append($("<label></label>").append(valueInput));
      if (element.phasePoints === true) {
        row.append($("<label></label>").append($("<span>in</span>"), phaseSelect));
      }
      break;
    }
    case "timer": {
      const input = newInput("number").addClass("result-editor-number").attr("min", 0).attr("step", 0.1)
        .val(raw.seconds || 0);
      input.on("input change", function () {
        const seconds = parseFloat(input.val());
        raw.seconds = isNaN(seconds) ? 0 : seconds;
        changed();
      });
      node.append($("<div class='result-editor-phases'></div>").append($("<label></label>").append(input, "seconds")));
      break;
    }
    default: {
      const input = newInput("number").addClass("result-editor-number")
        .attr("step", element.step !== undefined ? element.step : "any").val(raw.number || 0);
      if (element.min !== undefined) {
        input.attr("min", element.min);
      }
      if (element.max !== undefined) {
        input.attr("max", element.max);
      }
      input.on("input change", function () {
        const number = parseFloat(input.val());
        raw.number = isNaN(number) ? 0 : number;
        changed();
      });
      node.append(
        $("<div class='result-editor-phases'></div>").append($("<label></label>").append(input, element.unit || ""))
      );
    }
  }
  return node;
};

// Returns the label of a robot position of an alliance: its team number if the match has one there.
const robotLabel = function (allianceId, position) {
  const team = (editData.teams[allianceId] || [])[position - 1];
  return team ? "Team " + team : "Robot " + position;
};

// Builds the editors of a list of elements, grouped as the game groups them. values(element) returns the raw
// values to edit as a list of {label, raw}.
const buildElementEditors = function (container, elements, values) {
  const groups = [];
  elements.forEach(function (element) {
    if (!groups.includes(element.group)) {
      groups.push(element.group);
    }
  });
  groups.forEach(function (group) {
    const fieldset = $("<fieldset class='mb-3'></fieldset>").appendTo(container);
    fieldset.append($("<legend class='fs-5'></legend>").text(group));
    elements.filter(function (element) { return element.group === group; }).forEach(function (element) {
      const elementNode = $("<div class='result-editor-element'></div>").appendTo(fieldset);
      elementNode.append($("<h6 class='fw-bold mb-2'></h6>").text(element.name));
      const owners = values(element);
      const row = $("<div></div>").addClass(owners.length > 1 ? "result-editor-robots" : "").appendTo(elementNode);
      owners.forEach(function (owner) {
        const cell = $("<div></div>").appendTo(row);
        if (owner.label) {
          cell.append($("<div class='small text-muted'></div>").text(owner.label));
        }
        cell.append(buildValueEditor(element, owner.raw, scheduleSummaryRefresh));
      });
    });
  });
};

// Draws the list of an alliance's fouls. It is drawn again whenever a foul is added, removed or changes type,
// because the rules on offer depend on the type.
const renderFouls = function (alliance, container) {
  const view = GameView.view;
  const allianceState = editedState.alliances[alliance.id];
  container.empty();
  allianceState.fouls.forEach(function (foul, index) {
    const row = $("<div class='row g-2 align-items-center mb-2'></div>").appendTo(container);
    const typeSelect = newSelect(view.foulTypes.map(function (foulType) {
      return {value: foulType.id, text: foulType.name};
    }), foul.type);
    const robotChoices = [{value: 0, text: "No team"}];
    for (let position = 1; position <= view.allianceSize; position++) {
      robotChoices.push({value: position, text: robotLabel(alliance.id, position)});
    }
    const robotSelect = newSelect(robotChoices, foul.robot || 0);
    const ruleChoices = [{value: "", text: "No rule selected"}].concat(view.rules.filter(function (rule) {
      return rule.foul === foul.type;
    }).map(function (rule) {
      return {value: rule.id, text: rule.id + ": " + rule.name};
    }));
    const ruleSelect = newSelect(ruleChoices, foul.rule || "");
    const removeButton = $("<button type='button' class='btn-close' title='Remove this foul'></button>");

    typeSelect.on("change", function () {
      foul.type = typeSelect.val();
      // A rule belongs to a type of foul, so the one cited no longer applies.
      foul.rule = "";
      renderFouls(alliance, container);
      scheduleSummaryRefresh();
    });
    robotSelect.on("change", function () {
      foul.robot = parseInt(robotSelect.val(), 10);
      scheduleSummaryRefresh();
    });
    ruleSelect.on("change", function () {
      foul.rule = ruleSelect.val();
      scheduleSummaryRefresh();
    });
    removeButton.on("click", function () {
      allianceState.fouls.splice(index, 1);
      renderFouls(alliance, container);
      scheduleSummaryRefresh();
    });
    row.append(
      $("<div class='col-lg-3'></div>").append(typeSelect), $("<div class='col-lg-3'></div>").append(robotSelect),
      $("<div class='col-lg-5'></div>").append(ruleSelect), $("<div class='col-lg-1'></div>").append(removeButton)
    );
  });
};

// Builds the editor of one alliance: its own elements, its robots' elements, its fouls, its cards and whether it
// is disqualified.
const buildAllianceEditor = function (alliance) {
  const view = GameView.view;
  const allianceState = editedState.alliances[alliance.id];
  const slot = GameView.slot(alliance.id);
  const column = $("<div class='col-lg-6'></div>").appendTo("#allianceEditors");
  const card = $("<div class='card card-body mb-3'></div>").addClass("bg-" + slot).appendTo(column);
  card.append($("<h4></h4>").text(alliance.name));

  const elements = view.elements.filter(function (element) {
    return element.scope !== "field";
  }).sort(function (a, b) {
    return a.order - b.order;
  });
  buildElementEditors(card, elements, function (element) {
    if (element.scope === "alliance") {
      allianceState.elements[element.id] = allianceState.elements[element.id] || {};
      return [{label: "", raw: allianceState.elements[element.id]}];
    }
    return allianceState.robots.map(function (robot, index) {
      robot[element.id] = robot[element.id] || {};
      return {label: robotLabel(alliance.id, index + 1), raw: robot[element.id]};
    });
  });

  if (view.foulTypes.length > 0) {
    const fieldset = $("<fieldset class='mb-3'></fieldset>").appendTo(card);
    fieldset.append("<legend class='fs-5'>Fouls</legend>");
    const foulList = $("<div></div>").appendTo(fieldset);
    renderFouls(alliance, foulList);
    const addButton = $("<button type='button' class='btn btn-secondary btn-sm'>Add Foul</button>");
    addButton.on("click", function () {
      // A foul without an id is one the result does not have yet.
      allianceState.fouls.push({id: 0, type: view.foulTypes[0].id, robot: 0, rule: ""});
      renderFouls(alliance, foulList);
      scheduleSummaryRefresh();
    });
    fieldset.append(addButton);
  }

  if (view.cards.length > 0) {
    const fieldset = $("<fieldset class='mb-3'></fieldset>").appendTo(card);
    fieldset.append("<legend class='fs-5'>Cards</legend>");
    const row = $("<div class='result-editor-robots'></div>").appendTo(fieldset);
    const cardChoices = [{value: "", text: "None"}].concat(view.cards.map(function (gameCard) {
      return {value: gameCard.id, text: gameCard.name};
    }));
    for (let position = 1; position <= view.allianceSize; position++) {
      const select = newSelect(cardChoices, allianceState.cards[position] || "");
      select.on("change", function () {
        if (select.val() === "") {
          delete allianceState.cards[position];
        } else {
          allianceState.cards[position] = select.val();
        }
        scheduleSummaryRefresh();
      });
      row.append($("<div></div>").append(
        $("<div class='small text-muted'></div>").text(robotLabel(alliance.id, position)), select
      ));
    }
  }

  const disqualified = newInput("checkbox").prop("checked", allianceState.disqualified === true);
  disqualified.on("change", function () {
    allianceState.disqualified = disqualified.prop("checked");
    scheduleSummaryRefresh();
  });
  card.append($("<label class='mb-2'></label>").append(disqualified, " Alliance disqualified from this match"));

  // The summary of this alliance, which the server computes.
  const summaryCard = $("<div class='card card-body mb-3'></div>").addClass("bg-" + slot).appendTo(column);
  summaryCard.append("<h5 class='card-title'>Score Summary</h5>");
  summaryCard.append(
    $("<table class='table table-sm mb-0 score-summary-table'></table>").attr("data-summary-alliance", alliance.id)
  );
};

// Shows the result as the server computed it: the winner, and for each alliance its breakdown, score and ranking
// points.
const renderSummary = function (summary) {
  let winner = "Tie";
  if (summary.winner !== "") {
    winner = "Winner: " + summary.winnerName;
  }
  if (summary.tiebreak) {
    winner += " (decided by: " + summary.tiebreak + ")";
  }
  $("#resultWinner").text(winner);
  $("#summaryErrors").toggle(summary.errors.length > 0).text(summary.errors.join("; "));

  summary.alliances.forEach(function (alliance) {
    const table = $(`table[data-summary-alliance="${alliance.id}"]`).empty();
    const addRow = function (name, value, cssClass) {
      const row = $("<tr></tr>").addClass(cssClass || "");
      row.append($("<th scope='row'></th>").text(name));
      row.append($("<td class='text-end font-monospace'></td>").text(value));
      table.append(row);
    };
    alliance.breakdown.forEach(function (row) {
      addRow(row.name, row.text);
    });
    addRow(alliance.disqualified ? "Score (disqualified)" : "Score", alliance.score, "score-summary-total");
    if (!editData.isPlayoff) {
      alliance.rankingPoints.forEach(function (rankingPoint) {
        addRow(rankingPoint.name, rankingPoint.text);
      });
      addRow("Ranking Points", alliance.rpTotal, "score-summary-total");
    }
  });
};

const scheduleSummaryRefresh = function () {
  window.clearTimeout(summaryRefreshTimer);
  summaryRefreshTimer = window.setTimeout(refreshSummary, SUMMARY_REFRESH_DELAY_MS);
};

// Asks the server what the edited state computes to. Only the answer to the latest request is shown.
const refreshSummary = function () {
  const requestId = ++latestSummaryRequestId;
  $.ajax({
    url: `/match_review/${editData.matchId}/summary`,
    method: "POST",
    contentType: "application/json",
    data: JSON.stringify(editedState),
    success: function (data) {
      if (requestId === latestSummaryRequestId) {
        renderSummary(data);
      }
    },
  });
};

// Builds the editor from the data the page embeds.
const initResultEditor = function (data) {
  editData = data;
  // The raw state is edited in a copy shaped the way the server sends it; empty parts are left out of what it
  // sends, so they are filled in here.
  editedState = JSON.parse(JSON.stringify(data.state));
  editedState.field = editedState.field || {};
  editedState.alliances = editedState.alliances || {};
  GameView.set(data.game);
  const view = GameView.view;
  view.alliances.forEach(function (alliance) {
    const allianceState = editedState.alliances[alliance.id] = editedState.alliances[alliance.id] || {};
    allianceState.elements = allianceState.elements || {};
    allianceState.robots = allianceState.robots || [];
    while (allianceState.robots.length < view.allianceSize) {
      allianceState.robots.push({});
    }
    allianceState.fouls = allianceState.fouls || [];
    allianceState.cards = allianceState.cards || {};
  });

  // What is scored for the field as a whole comes first, then one column per alliance.
  const fieldElements = GameView.elementsFor("field");
  if (fieldElements.length > 0) {
    const card = $("<div class='card card-body mb-3'></div>").appendTo("#fieldEditor");
    card.append("<h4>Field</h4>");
    buildElementEditors(card, fieldElements, function (element) {
      editedState.field[element.id] = editedState.field[element.id] || {};
      return [{label: "", raw: editedState.field[element.id]}];
    });
  }
  view.alliances.forEach(buildAllianceEditor);
  renderSummary(data.summary);

  // Hijack the form submission to send the edited state in JSON form so that it's easier for the server to parse.
  $("#resultForm").on("submit", function () {
    $("#stateJson").val(JSON.stringify(editedState));
    return true;
  });
};
