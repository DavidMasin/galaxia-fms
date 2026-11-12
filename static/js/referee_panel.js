// Copyright 2023 Team 254. All Rights Reserved.
// Author: pat@patfairbank.com (Patrick Fairbank)
//
// Client-side logic for the referee interface. The alliances, the foul types, the cards and the scoring elements a
// referee may change come from the active game (see game_view.js); nothing here names any of them.

var websocket;
let foulsHashCode = 0;
let scoreIsReady = false;
let isPostMatch = false;
let isPlayoff = false;

// What the server has told us so far. Messages can arrive in any order, so each handler stores what it was given and
// the panel is drawn from whatever is there.
let lastMatchLoad = null;
let lastMatchTime = null;
let lastScore = null;
let lastScoreSeq = null;
let lastScoringStatus = null;
let lastArenaStatus = null;

// The controls for the scoring elements a referee may change, and the latest changes made to them.
let controls = [];
let changes = [];
const maxStoredChanges = 30;
const maxListedChanges = 4;
let changesSignature = "";
let messageTimer = null;

// Returns the team at a robot position of an alliance as the match load message describes it, or null.
const teamObjectAt = function (allianceId, robot) {
  if (lastMatchLoad === null) {
    return null;
  }
  return lastMatchLoad.Teams[ScoreControls.stationId(GameView.allianceIndex(allianceId), robot)] || null;
};

// Returns the team number at a robot position of an alliance, or 0 if the station is empty.
const teamAt = function (allianceId, robot) {
  const team = teamObjectAt(allianceId, robot);
  return team ? team.Id : 0;
};

// Returns the alliance's part of the latest score if it belongs to the game the panel was drawn from, or null.
const allianceState = function (allianceId) {
  if (lastScore === null || !GameView.isReady() || lastScore.gameHash !== GameView.view.hash) {
    return null;
  }
  const alliance = lastScore.alliances[allianceId];
  return alliance ? alliance.state : null;
};

const getClock = function () {
  if (lastMatchTime === null) {
    return {state: "PRE_MATCH", phaseIndex: -1, phaseTimeSec: 0, matchTimeSec: 0};
  }
  return {
    state: matchStates[lastMatchTime.MatchState],
    phaseIndex: lastMatchTime.PhaseIndex,
    phaseTimeSec: lastMatchTime.PhaseTimeSec,
    matchTimeSec: lastMatchTime.MatchTimeSec,
  };
};

// Shows a message at the top of the panel for a few seconds.
const showMessage = function (text) {
  window.clearTimeout(messageTimer);
  $("#panelMessage").text(text).show();
  messageTimer = window.setTimeout(function () {
    $("#panelMessage").hide();
  }, 5000);
};

// Sends a foul of the given type against an alliance to the server to add it to the list.
const addFoul = function (allianceId, foulType) {
  websocket.send("foul", {alliance: allianceId, type: foulType});
};

// Changes the type, the robot or the rule of a foul. changed holds the properties to change.
const updateFoul = function (allianceId, foulId, changed) {
  const state = allianceState(allianceId);
  const foul = state === null ? null : (state.fouls || []).find(function (candidate) {
    return candidate.id === foulId;
  });
  if (!foul) {
    return;
  }
  const updated = {
    alliance: allianceId, update: foulId, type: foul.type, robot: foul.robot || 0, rule: foul.rule || "",
  };
  Object.assign(updated, changed);
  websocket.send("foul", updated);
};

// Removes the foul with the given id from the list.
const removeFoul = function (allianceId, foulId) {
  websocket.send("foul", {alliance: allianceId, remove: foulId});
};

// Returns the card to ask for when a team's card button is pressed: the cards of the game are stepped through in
// the order the game lists them, and after the last one the card is cleared. It follows the two rules the server
// applies. A card that names a second_becomes card turns into that card for a team that carries a card from an
// earlier match or already holds the same card, so asking for it is how the second card is reached. A card that
// disqualifies cannot be a team's first card, so it is skipped for a team that has none.
const nextCard = function (current, carried) {
  const cards = GameView.view.cards;
  const start = cards.findIndex(function (card) {
    return card.id === current;
  });
  for (let i = start + 1; i < cards.length; i++) {
    const card = cards[i];
    const becomesSecond = Boolean(card.secondBecomes) && (carried || current === card.id);
    if ((becomesSecond ? card.secondBecomes : card.id) === current) {
      continue;
    }
    if (!becomesSecond && card.disqualifies && current === "" && !carried) {
      continue;
    }
    return card.id;
  }
  return "";
};

// Handles a press of a team's or a playoff alliance's button: after the match it steps through the cards, and
// before and during it the button of a station bypasses or disables the robot there.
const pressCardButton = function (cardButton) {
  const button = $(cardButton);
  if (isPostMatch) {
    const card = nextCard(button.attr("data-card") || "", button.attr("data-carried") === "true");
    websocket.send(
      "card", {alliance: button.attr("data-alliance"), robot: parseInt(button.attr("data-position")), card: card}
    );
    return;
  }
  if (!button.hasClass("station-card")) {
    return;
  }

  // Toggle bypass.
  const isDisabled = button.hasClass("bypassed-status");
  const team = button.attr("data-team");
  $("#confirmBypassTitle").text(`${isDisabled ? "Enable" : "Disable"} ${team}?`);
  $("#confirmBypassAction").text(isDisabled ? "Enable" : "Disable");
  $("#confirmBypass").attr("data-station", button.attr("data-station"));

  if (team === "0") {
    toggleBypass();
  } else {
    $("#confirmBypass").modal("show");
  }
};

const toggleBypass = function () {
  const station = $("#confirmBypass").attr("data-station");
  websocket.send("toggleBypass", station);
};

// Sends a websocket message to signal to the volunteers that they may enter the field.
var signalVolunteers = function () {
  websocket.send("signalVolunteers");
};

// Sends a websocket message to signal to the teams that they may enter the field.
var signalReset = function () {
  websocket.send("signalReset");
};

// Shows confirmation modal if not all scores are ready, otherwise directly commits and posts.
var confirmCommit = function () {
  if (scoreIsReady) {
    commitAndPost();
    return;
  }

  $("#confirmCommit").modal("show");
};

// Commits the score and posts results to the audience.
var commitAndPost = function () {
  websocket.send("commitAndPost");
};

// Returns the alliances in the order the panel lays them out: the far side of the field first, as a referee facing
// the field sees it.
const panelAlliances = function () {
  return GameView.alliances().slice().reverse();
};

// Builds everything that depends on the game: the card buttons, the foul buttons and the scoring controls. Called
// when the game arrives or changes and when a match is loaded.
const rebuild = function () {
  if (!GameView.isReady()) {
    return;
  }
  const view = GameView.view;

  // One box of card buttons per alliance: a button for the alliance as a whole, which is used in the playoffs,
  // and one for each station.
  const cardsContainer = $("#allianceCards").empty();
  GameView.alliances().forEach(function (alliance, index) {
    const box = $("<div class='alliance-cards'></div>").attr("data-alliance-index", index).appendTo(cardsContainer);
    const allianceButton = $("<button type='button' class='team-card playoff-alliance-card'></button>")
      .attr("data-alliance", alliance.id).attr("data-position", 0);
    allianceButton.on("click", function () { pressCardButton(this); });
    box.append(allianceButton);
    for (let position = 1; position <= view.allianceSize; position++) {
      const stationButton = $("<div class='team-card station-card'></div>").attr("data-alliance", alliance.id)
        .attr("data-position", position).attr("data-station", ScoreControls.stationId(index, position));
      stationButton.on("click", function () { pressCardButton(this); });
      box.append(stationButton);
    }
  });

  // One button per alliance and type of foul.
  const foulButtons = $("#foulButtons").empty();
  panelAlliances().forEach(function (alliance) {
    const row = $("<div class='foul-button-row'></div>").appendTo(foulButtons);
    view.foulTypes.forEach(function (foulType) {
      const button = $("<div class='foul-button'></div>").attr("data-alliance-index", GameView.allianceIndex(alliance.id))
        .text(alliance.name + " " + foulType.name);
      button.on("click", function () { addFoul(alliance.id, foulType.id); });
      row.append(button);
    });
  });

  // The scoring elements a referee may change, for the field and then for each alliance.
  const elementsContainer = $("#refereeElements").empty();
  controls = [];
  const refereeElements = view.elements.filter(function (element) {
    return !element.hidden && element.edit.includes("referee");
  }).sort(function (a, b) {
    return a.order - b.order;
  });
  const sendScore = function (command) {
    websocket.send("score", command);
  };
  const fieldElements = refereeElements.filter(function (element) { return element.scope === "field"; });
  const allianceElements = refereeElements.filter(function (element) { return element.scope !== "field"; });
  if (fieldElements.length > 0) {
    controls = controls.concat(
      ScoreControls.buildSection(elementsContainer, {allianceId: "", elements: fieldElements}, teamAt, sendScore)
    );
  }
  if (allianceElements.length > 0) {
    panelAlliances().forEach(function (alliance) {
      controls = controls.concat(ScoreControls.buildSection(
        elementsContainer, {allianceId: alliance.id, elements: allianceElements}, teamAt, sendScore
      ));
    });
  }

  foulsHashCode = 0;
  refresh();
};

// Brings the whole panel up to date with what the server has sent.
const refresh = function () {
  if (!GameView.isReady()) {
    return;
  }
  const clock = getClock();
  isPostMatch = clock.state === "POST_MATCH";
  isPlayoff = lastMatchLoad !== null && lastMatchLoad.Match.Type === matchTypePlayoff;

  let title = "Cards";
  if (!isPostMatch) {
    title = clock.state === "PRE_MATCH" ? "Bypass" : "Disable";
  }
  $("#teamTitle").text(title);
  $(".control-button").attr("data-enabled", isPostMatch);
  $(".playoff-alliance-card").toggle(isPlayoff && isPostMatch);
  $(".station-card").toggle(!isPlayoff || !isPostMatch);

  // Card buttons.
  GameView.alliances().forEach(function (alliance, index) {
    const state = allianceState(alliance.id);
    const cards = state === null ? {} : state.cards || {};
    let allianceCard = "";
    for (let position = 1; position <= GameView.view.allianceSize; position++) {
      const team = teamObjectAt(alliance.id, position);
      const station = ScoreControls.stationId(index, position);
      const card = cards[position] || "";
      allianceCard = allianceCard || card;
      const stationStatus = lastArenaStatus === null ? null : lastArenaStatus.AllianceStations[station];
      const button = $(`.station-card[data-station="${station}"]`);
      button.text(team ? team.Id : "-").attr("data-team", team ? team.Id : 0);
      button.attr("data-carried", team !== null && !isPlayoff && team.YellowCard === true);
      button.toggleClass("bypassed-status", Boolean(stationStatus && stationStatus.Bypass) && !isPostMatch);
      setCardAppearance(button, card);
    }
    const allianceButton = $(`.playoff-alliance-card[data-alliance="${alliance.id}"]`);
    if (lastMatchLoad !== null) {
      const match = lastMatchLoad.Match;
      allianceButton.text("Alliance " + [match.PlayoffRedAlliance, match.PlayoffBlueAlliance][index]);
      const carried = [lastMatchLoad.PlayoffRedAllianceYellowCard, lastMatchLoad.PlayoffBlueAllianceYellowCard][index];
      allianceButton.attr("data-carried", carried === true);
      if (isPlayoff) {
        $(`.station-card[data-alliance="${alliance.id}"]`).attr("data-carried", carried === true);
      }
    }
    setCardAppearance(allianceButton, allianceCard);
  });

  // Fetch the foul list again if any foul changed.
  const fouls = GameView.alliances().map(function (alliance) {
    const state = allianceState(alliance.id);
    return state === null ? [] : state.fouls || [];
  });
  const newFoulsHashCode = hashObject([fouls, lastMatchLoad === null ? null : lastMatchLoad.Teams]);
  if (newFoulsHashCode !== foulsHashCode) {
    foulsHashCode = newFoulsHashCode;
    fetch("/panels/referee/foul_list")
      .then(response => response.text())
      .then(html => $("#foulList").html(html));
  }

  // Scoring controls.
  controls.forEach(function (entry) {
    const score = lastScore !== null && lastScore.gameHash === GameView.view.hash ? lastScore : null;
    const raw = ScoreControls.rawValue(score, entry.element, entry.allianceId, entry.robot);
    entry.control.update(raw, score !== null && ScoreControls.editable(entry.element, "referee", clock), clock);
  });
  renderChanges();
  renderScoringStatus();
};

// Shows a card on a team's button: the button takes the colour the game gives the card.
const setCardAppearance = function (button, cardId) {
  const card = GameView.card(cardId);
  button.attr("data-card", cardId);
  button.css("background-color", card === null ? "" : card.color);
  button.attr("title", card === null ? "" : card.name);
};

// Lists the latest changes to the scoring elements shown here, newest first, each with a button to undo it.
const renderChanges = function () {
  if (!GameView.isReady()) {
    return;
  }
  const clock = getClock();
  const canUndo = clock.state === "IN_MATCH" || clock.state === "POST_MATCH";
  const listed = changes.filter(function (entry) {
    return controls.some(function (control) {
      return control.element.id === entry.change.element;
    });
  }).slice(-maxListedChanges).reverse();

  // The list is only drawn again when it would look different, so that a button is never replaced under a finger.
  const signature = JSON.stringify([canUndo, GameView.view.hash, listed.map(function (entry) {
    return [entry.change.seq, entry.undone];
  })]);
  if (signature === changesSignature) {
    return;
  }
  changesSignature = signature;
  const list = $("#changes").empty();
  listed.forEach(function (entry) {
    const item = $("<li class='change'></li>").attr("data-undone", entry.undone);
    item.append($("<span class='change-text'></span>").text(ScoreControls.describeChange(entry.change, teamAt)));
    if (entry.undone) {
      item.append($("<span class='change-undone'>undone</span>"));
    } else {
      const button = $("<button type='button' class='change-undo'>Undo</button>").prop("disabled", !canUndo);
      button.on("click", function () {
        websocket.send("undo", {seq: entry.change.seq});
      });
      item.append(button);
    }
    list.append(item);
  });
};

// Shows which scoring panels have committed, with one badge per panel position of the game.
const renderScoringStatus = function () {
  const data = lastScoringStatus;
  if (data === null || !GameView.isReady()) {
    return;
  }
  if (data.RefereeScoreReady) {
    $("#commitButton").attr("data-enabled", false);
  }
  const container = $("#scoringStatuses").empty();
  ScoreControls.panels(GameView.view).forEach(function (panel) {
    const status = data.PositionStatuses[panel.id];
    if (!status) {
      return;
    }
    container.append(
      $("<div class='scoring-status'></div>").text(`${panel.name} ${status.NumPanelsReady}/${status.NumPanels}`)
        .attr("data-present", status.NumPanels > 0).attr("data-ready", status.Ready)
    );
  });

  scoreIsReady = Object.values(data.PositionStatuses).every(status => status.Ready);

  // Make the button visually distinct if not all refs have committed.
  // HR can still press the button with confirm modal.
  $("#commitButton").toggleClass("disabled", !scoreIsReady);
};

// Handles a websocket message to update the teams for the current match.
var handleMatchLoad = function (data) {
  lastMatchLoad = data;
  $("#matchName").text(data.Match.LongName);
  rebuild();
};

// Handles a websocket message to update the match status.
const handleMatchTime = function (data) {
  lastMatchTime = data;
  refresh();
};

// Handles a websocket message to update the realtime scoring fields.
const handleRealtimeScore = function (data) {
  if (lastScoreSeq !== null && data.seq < lastScoreSeq) {
    // The sequence only starts over when a match is loaded, and its changes are then no longer there to undo.
    changes = changes.filter(function (entry) {
      return entry.change.seq <= data.seq;
    });
  }
  lastScoreSeq = data.seq;
  lastScore = data;
  refresh();
};

// Handles a websocket message about one score change: a new one, or one that was undone or brought back.
const handleScoreChange = function (data) {
  const existing = changes.find(function (entry) {
    return entry.change.seq === data.change.seq;
  });
  if (existing) {
    existing.undone = data.undone;
  } else {
    changes.push({change: data.change, undone: data.undone});
    changes.sort(function (a, b) {
      return a.change.seq - b.change.seq;
    });
    changes = changes.slice(-maxStoredChanges);
  }
  renderChanges();
};

// Handles a websocket message to update the scoring commit status.
const handleScoringStatus = function (data) {
  lastScoringStatus = data;
  renderScoringStatus();
};

const handleArenaStatus = function (data) {
  lastArenaStatus = data;
  refresh();
};

// Produces a hash code of the given object for use in equality comparisons.
const hashObject = function (object) {
  const s = JSON.stringify(object);
  let h = 0;
  for (let i = 0; i < s.length; i++) {
    h = Math.imul(31, h) + s.charCodeAt(i) | 0;
  }
  return h;
};

$(function () {
  // Read the configuration for this display from the URL query string.
  var urlParams = new URLSearchParams(window.location.search);
  $(".headRef-dependent").attr("data-hr", urlParams.get("hr"));
  $("#panelMessage").hide();

  // Draw the panel when the game arrives, and again whenever it changes.
  GameView.onChange(rebuild);

  // Set up the websocket back to the server.
  websocket = new CheesyWebsocket("/panels/referee/websocket", {
    game: function (event) {
      handleGame(event.data);
    },
    matchLoad: function (event) {
      handleMatchLoad(event.data);
    },
    matchTime: function (event) {
      handleMatchTime(event.data);
    },
    realtimeScore: function (event) {
      handleRealtimeScore(event.data);
    },
    scoreChange: function (event) {
      handleScoreChange(event.data);
    },
    scoringStatus: function (event) {
      handleScoringStatus(event.data);
    },
    arenaStatus: function (event) {
      handleArenaStatus(event.data);
    },
    error: function (event) {
      // A refused call is something to read and carry on from, not something to stop for.
      showMessage(event.data);
    },
  });
});
