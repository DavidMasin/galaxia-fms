// Copyright 2014 Team 254. All Rights Reserved.
// Author: pat@patfairbank.com (Patrick Fairbank)
// Author: ian@yann.io (Ian Thompson)
//
// Client-side logic for the scoring interface. The panel is generated from the active game: which elements it shows
// is decided by the game's layout for this panel position, and each control by the element's widget.

var websocket;
let position;
let committed = false;

// What the server has told us so far. Messages can arrive in any order, so each handler stores what it was given and
// the panel is drawn from whatever is there.
let lastScore = null;
let lastMatchTime = null;
let teams = {};
let lastScoreSeq = null;

// The controls on the page, as built by ScoreControls.buildSection, and the latest score changes by sequence number.
let controls = [];
let changes = [];
const maxStoredChanges = 30;
const maxListedChanges = 6;
let changesSignature = "";
let messageTimer = null;

// Returns the team number at a robot position of an alliance, or 0 if the station is empty.
const teamAt = function (allianceId, robot) {
  const team = teams[ScoreControls.stationId(GameView.allianceIndex(allianceId), robot)];
  return team ? team.Id : 0;
};

// Returns the clock as the controls need it.
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

// Shows a message at the top of the panel. A message that is not sticky goes away by itself.
const showMessage = function (text, sticky) {
  window.clearTimeout(messageTimer);
  $("#panelMessage").text(text).toggle(text !== "");
  if (text !== "" && !sticky) {
    messageTimer = window.setTimeout(function () {
      $("#panelMessage").hide();
    }, 5000);
  }
};

// Returns this panel's entry in the game's list of scoring panels, or null if the game has no such position.
const getPanel = function () {
  if (!GameView.isReady()) {
    return null;
  }
  return ScoreControls.panels(GameView.view).find(function (panel) {
    return panel.id === position;
  }) || null;
};

// Sends one change of a scoring element to the server.
const sendScore = function (command) {
  websocket.send("score", command);
};

// Builds the controls from the game. Called when the game arrives or changes and when a match is loaded, because
// the controls of robot elements are labelled with team numbers.
const rebuild = function () {
  const container = $("#controls").empty();
  controls = [];
  const panel = getPanel();
  if (!GameView.isReady()) {
    return;
  }
  if (panel === null) {
    showMessage("The game " + GameView.view.name + " has no scoring panel \"" + position + "\".", true);
    return;
  }
  showMessage("", true);
  $("#panelName").text(panel.name);
  ScoreControls.panelSections(GameView.view, panel).forEach(function (section) {
    controls = controls.concat(ScoreControls.buildSection(container, section, teamAt, sendScore));
  });
  if (controls.length === 0) {
    showMessage("The game " + GameView.view.name + " gives this panel nothing to score.", true);
  }
  refresh();
};

// Brings every control and the commit button up to date with the score and the clock.
const refresh = function () {
  if (!GameView.isReady()) {
    return;
  }
  const clock = getClock();
  // A score that belongs to another game than the one the controls were built from cannot be shown with them.
  const score = lastScore !== null && lastScore.gameHash === GameView.view.hash ? lastScore : null;
  controls.forEach(function (entry) {
    const raw = ScoreControls.rawValue(score, entry.element, entry.allianceId, entry.robot);
    const enabled = score !== null && !committed && ScoreControls.editable(entry.element, "scorer", clock);
    entry.control.update(raw, enabled, clock);
  });
  $("#commit").prop("disabled", committed || clock.state !== "POST_MATCH");
  $("#commit").text(committed ? "Committed" : "Commit");
  renderClock();
  renderChanges();
};

// Says whether a score change concerns something this panel shows.
const isChangeShown = function (change) {
  return controls.some(function (entry) {
    return entry.element.id === change.element &&
      (entry.element.scope === "field" || entry.allianceId === change.alliance) &&
      (entry.element.scope !== "robot" || entry.robot === change.robot);
  });
};

// Lists the latest changes to what this panel shows, newest first, each with a button to undo it.
const renderChanges = function () {
  if (!GameView.isReady()) {
    return;
  }
  const clock = getClock();
  const canUndo = !committed && (clock.state === "IN_MATCH" || clock.state === "POST_MATCH");
  const listed = changes.filter(function (entry) {
    return isChangeShown(entry.change);
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

// Handles a websocket message to update the teams for the current match.
const handleMatchLoad = function (data) {
  $("#matchName").text(data.Match.LongName);
  teams = data.Teams || {};
  rebuild();
};

// Handles a websocket message to update the match status.
const handleMatchTime = function (data) {
  lastMatchTime = data;
  if (matchStates[data.MatchState] !== "POST_MATCH") {
    // A commit only lasts until the next match.
    committed = false;
  }
  refresh();
};

// Shows the state of the match, the name of the running phase and the countdown.
const renderClock = function () {
  if (lastMatchTime === null) {
    return;
  }
  translateMatchTime(lastMatchTime, function (matchState, matchStateText, countdownSec, phase) {
    $("#matchState").text(matchStateText);
    $("#phaseName").text(phase === null ? "" : phase.name);
    $("#matchTime").text(getCountdownString(countdownSec));
  });
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

// Clear any local ephemeral state that is not maintained by the server
const resetLocalState = function () {
  committed = false;
  changes = [];
  lastScoreSeq = null;
  refresh();
};

// Sends a websocket message to indicate that the score for this alliance is ready.
const commitMatchScore = function () {
  websocket.send("commitMatch");

  committed = true;
  refresh();
};

$(function () {
  position = decodeURIComponent(window.location.pathname.split("/").slice(-1)[0]);
  $("#panelMessage").hide();

  // Draw the panel when the game arrives, and again whenever it changes.
  GameView.onChange(rebuild);

  // Set up the websocket back to the server.
  websocket = new CheesyWebsocket("/panels/scoring/" + position + "/websocket", {
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
    resetLocalState: function (event) {
      resetLocalState();
    },
    error: function (event) {
      // A refused change is something to read and carry on from, not something to stop for.
      showMessage(event.data, false);
    },
  });
});
