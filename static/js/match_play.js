// Copyright 2014 Team 254. All Rights Reserved.
// Author: pat@patfairbank.com (Patrick Fairbank)
//
// Client-side logic for the match play page.

var websocket;
let scoreIsReady;
let isReplay;
const lowBatteryThreshold = 8;

// The value of model.MatchType for a test match.
const testMatchType = 0;

// The latest of each message whose presentation depends on the active game. They are kept so that the page can be
// drawn again when the game arrives or changes, whatever order the messages came in.
let lastMatchTime = null;
let lastRealtimeScore = null;
let lastScoringStatus = null;
let lastMatchType = null;

// Sends a websocket message to load the specified match.
const loadMatch = function (matchId) {
  websocket.send("loadMatch", {matchId: matchId});
}

// Sends a websocket message to load the results for the specified match into the display buffer.
const showResult = function (matchId) {
  websocket.send("showResult", {matchId: matchId});
}

// Sends a websocket message to load all teams into their respective alliance stations.
const substituteTeams = function (team, position) {
  const teams = {
    Red1: getTeamNumber("R1"),
    Red2: getTeamNumber("R2"),
    Red3: getTeamNumber("R3"),
    Blue1: getTeamNumber("B1"),
    Blue2: getTeamNumber("B2"),
    Blue3: getTeamNumber("B3"),
  };

  websocket.send("substituteTeams", teams);
};

// Sends a websocket message to toggle the bypass status for an alliance station.
const toggleBypass = function (station) {
  websocket.send("toggleBypass", station);
};

// Sends a websocket message to start the match.
const startMatch = function () {
  websocket.send("startMatch",
    {muteMatchSounds: $("#muteMatchSounds").prop("checked")});
};

// Sends a websocket message to abort the match.
const abortMatch = function () {
  websocket.send("abortMatch");
};

// Sends a websocket message to signal to the volunteers that they may enter the field.
const signalVolunteers = function () {
  websocket.send("signalVolunteers");
};

// Sends a websocket message to signal to the teams that they may enter the field.
const signalReset = function () {
  websocket.send("signalReset");
};

// Sends a websocket message to commit and post the match score, and load the next match.
const commitAndPost = function () {
  websocket.send("commitAndPost");
};

// Sends a websocket message to discard the match score and load the next match.
const discardResults = function () {
  websocket.send("discardResults");
};

// Switches the audience display to the match intro screen.
const showOverlay = function () {
  $("input[name=audienceDisplay][value=intro]").prop("checked", true);
  setAudienceDisplay();
  $("#showOverlay").prop("disabled", true);
}

// Sends a websocket message to change what the audience display is showing.
const setAudienceDisplay = function () {
  websocket.send("setAudienceDisplay", $("input[name=audienceDisplay]:checked").val());
};

// Sends a websocket message to change what the alliance station display is showing.
const setAllianceStationDisplay = function () {
  websocket.send("setAllianceStationDisplay", $("input[name=allianceStationDisplay]:checked").val());
};

// Sends a websocket message to start the timeout.
const startTimeout = function () {
  const duration = $("#timeoutDuration").val().split(":");
  let durationSec = parseFloat(duration[0]);
  if (duration.length > 1) {
    durationSec = durationSec * 60 + parseFloat(duration[1]);
  }
  websocket.send("startTimeout", {
    Description: $("#timeoutDescription").val(),
    NextMatchName: $("#timeoutNextMatchText").val(),
    DurationSec: durationSec,
  });
};

// Sends a websocket message to update timeout display text.
const setTimeoutDisplay = function () {
  websocket.send("setTimeoutDisplay", {
    Description: $("#timeoutDescription").val(),
    NextMatchName: $("#timeoutNextMatchText").val(),
  });
};

const confirmCommit = function () {
  if (isReplay || !scoreIsReady) {
    // Show the appropriate message(s) in the confirmation dialog.
    $("#confirmCommitReplay").css("display", isReplay ? "block" : "none");
    $("#confirmCommitNotReady").css("display", scoreIsReady ? "none" : "block");
    $("#confirmCommitResults").modal("show");
  } else {
    commitAndPost();
  }
};

// Sends a websocket message to specify a custom name for the current test match.
const setTestMatchName = function () {
  websocket.send("setTestMatchName", $("#testMatchName").val());
};

// Returns the integer team number entered into the team number input box for the given station, or 0 if it is empty.
const getTeamNumber = function (station) {
  const teamId = $(`#status${station} .team-number`).val().trim();
  return teamId ? parseInt(teamId) : 0;
}

const updateStartMatchTooltip = function (conditions) {
  const tooltipElement = document.getElementById("startMatchTooltip");
  const button = document.getElementById("startMatch");
  const tooltip = bootstrap.Tooltip.getInstance(tooltipElement);

  if (!conditions || conditions.length === 0) {
    button.style.pointerEvents = "";
    tooltipElement.removeAttribute("tabindex");
    delete tooltipElement.dataset.tooltipText;
    if (tooltip) {
      tooltip.dispose();
    }
    return;
  }

  button.style.pointerEvents = "none";
  tooltipElement.setAttribute("tabindex", "0");
  const tooltipText = `Unsatisfied conditions:\n• ${conditions.join("\n• ")}`;
  if (tooltipElement.dataset.tooltipText === tooltipText) {
    return;
  }
  tooltipElement.dataset.tooltipText = tooltipText;

  if (tooltip) {
    tooltip.setContent({".tooltip-inner": tooltipText});
  } else {
    new bootstrap.Tooltip(tooltipElement, {
      customClass: "start-match-tooltip",
      title: tooltipText,
    });
  }
}

// Handles a websocket message to update the team connection status.
const handleArenaStatus = function (data) {
  // Update the team status view.
  $.each(data.AllianceStations, function (station, stationStatus) {
    const wifiStatus = stationStatus.WifiStatus;
    $("#status" + station + " .radio-status").text(wifiStatus.TeamId);

    if (stationStatus.DsConn) {
      // Format the driver station status box.
      const dsConn = stationStatus.DsConn;
      $("#status" + station + " .ds-status").attr("data-status-ok", dsConn.DsLinked);
      if (dsConn.DsLinked) {
        $("#status" + station + " .ds-status").text(wifiStatus.MBits.toFixed(2) + "Mb");
      } else {
        $("#status" + station + " .ds-status").text("");
      }

      // Format the robot status box.
      const robotOkay = dsConn.BatteryVoltage > lowBatteryThreshold && dsConn.RobotLinked;
      $("#status" + station + " .robot-status").attr("data-status-ok", robotOkay);
      if (stationStatus.DsConn.SecondsSinceLastRobotLink > 1 && stationStatus.DsConn.SecondsSinceLastRobotLink < 1000) {
        $("#status" + station + " .robot-status").text(stationStatus.DsConn.SecondsSinceLastRobotLink.toFixed());
      } else {
        $("#status" + station + " .robot-status").text(dsConn.BatteryVoltage.toFixed(1) + "V");
      }
    } else {
      $("#status" + station + " .ds-status").attr("data-status-ok", "");
      $("#status" + station + " .robot-status").attr("data-status-ok", "");
      $("#status" + station + " .robot-status").text("");
    }

    // Format the radio status box according to whether the AP is configured with the correct SSID and the connection
    // status of the robot radio.
    const expectedTeamId = stationStatus.Team ? stationStatus.Team.Id : 0;
    let radioStatus = 0;
    if (expectedTeamId === wifiStatus.TeamId) {
      if (wifiStatus.RadioLinked || stationStatus.DsConn?.RobotLinked) {
        radioStatus = 2;
      } else {
        radioStatus = 1;
      }
    }
    $(`#status${station} .radio-status`).attr("data-status-ternary", radioStatus);

    if (stationStatus.EStop) {
      $("#status" + station + " .bypass-status").attr("data-status-ok", false);
      $("#status" + station + " .bypass-status").text("ES");
    } else if (stationStatus.AStop) {
      $("#status" + station + " .bypass-status").attr("data-status-ok", true);
      $("#status" + station + " .bypass-status").text("AS");
    } else if (stationStatus.Bypass) {
      $("#status" + station + " .bypass-status").attr("data-status-ok", false);
      $("#status" + station + " .bypass-status").text("B");
    } else {
      $("#status" + station + " .bypass-status").attr("data-status-ok", true);
      $("#status" + station + " .bypass-status").text("");
    }
  });

  // Enable/disable the buttons based on the current match state.
  switch (matchStates[data.MatchState]) {
    case "PRE_MATCH":
      $("#startMatch").prop("disabled", !data.CanStartMatch);
      $("#abortMatch").prop("disabled", true);
      $("#signalVolunteers").prop("disabled", false);
      $("#signalReset").prop("disabled", false);
      $("#fieldResetRadio").prop("disabled", false);
      $("#commitResults").prop("disabled", true);
      $("#discardResults").prop("disabled", true);
      $("#editResults").prop("disabled", true);
      $("#startTimeout").prop("disabled", false);
      break;
    case "START_MATCH":
    case "IN_MATCH":
      $("#showOverlay").prop("disabled", true);
      $("#introRadio").prop("disabled", true);
      $("#scoreRadio").prop("disabled", true);
      $("#startMatch").prop("disabled", true);
      $("#abortMatch").prop("disabled", false);
      $("#signalVolunteers").prop("disabled", true);
      $("#signalReset").prop("disabled", true);
      $("#fieldResetRadio").prop("disabled", true);
      $("#commitResults").prop("disabled", true);
      $("#discardResults").prop("disabled", true);
      $("#editResults").prop("disabled", true);
      $("#startTimeout").prop("disabled", true);
      break;
    case "POST_MATCH":
      $("#showOverlay").prop("disabled", true);
      $("#introRadio").prop("disabled", true);
      $("#scoreRadio").prop("disabled", true);
      $("#startMatch").prop("disabled", true);
      $("#abortMatch").prop("disabled", true);
      $("#signalVolunteers").prop("disabled", false);
      $("#signalReset").prop("disabled", false);
      $("#fieldResetRadio").prop("disabled", false);
      $("#commitResults").prop("disabled", false);
      $("#discardResults").prop("disabled", false);
      $("#editResults").prop("disabled", false);
      $("#startTimeout").prop("disabled", true);
      break;
    case "TIMEOUT_ACTIVE":
      $("#showOverlay").prop("disabled", true);
      $("#introRadio").prop("disabled", true);
      $("#scoreRadio").prop("disabled", false);
      $("#startMatch").prop("disabled", true);
      $("#abortMatch").prop("disabled", false);
      $("#signalVolunteers").prop("disabled", true);
      $("#signalReset").prop("disabled", true);
      $("#fieldResetRadio").prop("disabled", false);
      $("#commitResults").prop("disabled", true);
      $("#discardResults").prop("disabled", true);
      $("#editResults").prop("disabled", true);
      $("#startTimeout").prop("disabled", true);
      break;
    case "POST_TIMEOUT":
      $("#showOverlay").prop("disabled", false);
      $("#introRadio").prop("disabled", false);
      $("#scoreRadio").prop("disabled", false);
      $("#startMatch").prop("disabled", true);
      $("#abortMatch").prop("disabled", true);
      $("#signalVolunteers").prop("disabled", true);
      $("#signalReset").prop("disabled", true);
      $("#fieldResetRadio").prop("disabled", false);
      $("#commitResults").prop("disabled", true);
      $("#discardResults").prop("disabled", true);
      $("#editResults").prop("disabled", true);
      $("#startTimeout").prop("disabled", true);
      break;
  }
  updateStartMatchTooltip(data.StartMatchConditions);

  $("#accessPointStatus").attr("data-status", data.AccessPointStatus);
  $("#switchStatus").attr("data-status", data.SwitchStatus);
  $("#redSCCStatus").attr("data-status", data.RedSCCStatus);
  $("#blueSCCStatus").attr("data-status", data.BlueSCCStatus);

  if (data.PlcIsHealthy) {
    $("#plcStatus").text("Connected");
    $("#plcStatus").attr("data-ready", true);
  } else {
    $("#plcStatus").text("Not Connected");
    $("#plcStatus").attr("data-ready", false);
  }
  $("#fieldEStop").attr("data-ready", !data.FieldEStop);
  $.each(data.PlcArmorBlockStatuses, function (name, status) {
    $("#plc" + name + "Status").attr("data-ready", status);
  });
  $("#ftaReady").attr("data-ready", data.IsFtaReady);
};

// Handles a websocket message to update the teams for the current match.
const handleMatchLoad = function (data) {
  isReplay = data.IsReplay;

  fetch("/match_play/match_load")
    .then(response => response.text())
    .then(html => $("#matchListColumn").html(html));

  $("#matchName").text(data.Match.LongName);
  $("#testMatchName").val(data.Match.LongName);
  $("#timeoutDescription").val(data.BreakDescription || "Field Break");
  $("#timeoutNextMatchText").val(data.BreakNextMatchName || "");
  $("#testMatchSettings").toggle(data.Match.Type === testMatchType);
  lastMatchType = data.Match.Type;
  renderScoreSummary();
  $.each(data.Teams, function (station, team) {
    const teamId = $(`#status${station} .team-number`);
    teamId.val(team ? team.Id : "");
    teamId.prop("disabled", !data.AllowSubstitution);
  });
  $("#playoffRedAllianceInfo").html(formatPlayoffAllianceInfo(data.Match.PlayoffRedAlliance, data.RedOffFieldTeams));
  $("#playoffBlueAllianceInfo").html(formatPlayoffAllianceInfo(data.Match.PlayoffBlueAlliance, data.BlueOffFieldTeams));

  $("#substituteTeams").prop("disabled", true);
  $("#showOverlay").prop("disabled", false);
  $("#introRadio").prop("disabled", false);
  $("#muteMatchSounds").prop("checked", false);
}

// Handles a websocket message to update the match time countdown.
const handleMatchTime = function (data) {
  lastMatchTime = data;
  translateMatchTime(data, function (matchState, matchStateText, countdownSec, phase) {
    // While a phase is running, say which one: a game may split a stretch of the match into several.
    $("#matchState").text(phase === null ? matchStateText : matchStateText + " \u2013 " + phase.name);
    $("#matchTime").text(countdownSec);
  });
};

// Handles a websocket message to update the match score.
const handleRealtimeScore = function (data) {
  lastRealtimeScore = data;
  renderScoreSummary();
};

// Returns the alliance's part of the latest score if it belongs to the active game, or null.
const getAllianceScore = function (allianceId) {
  if (lastRealtimeScore === null || !GameView.isReady() || lastRealtimeScore.gameHash !== GameView.view.hash) {
    return null;
  }
  return lastRealtimeScore.alliances[allianceId] || null;
};

// Shows the score of each alliance and, below the controls, how the game breaks it down: the breakdown rows and
// ranking points come from the game, so this works for any of them.
const renderScoreSummary = function () {
  if (!GameView.isReady()) {
    return;
  }
  const view = GameView.view;
  const container = $("#scoreSummary").empty();
  // The second alliance is on the left, as it is in the list of teams above.
  GameView.alliances().slice().reverse().forEach(function (alliance) {
    const slot = GameView.slot(alliance.id);
    const score = getAllianceScore(alliance.id);
    $(`#${slot}Score`).text(score === null ? "\u00a0" : GameView.formatNumber(score.score));

    const table = $("<table class='table table-sm mb-0 score-summary-table'></table>");
    const addRow = function (name, value, cssClass) {
      const row = $("<tr></tr>").addClass(cssClass || "");
      row.append($("<th scope='row'></th>").text(name));
      row.append($("<td class='text-end font-monospace'></td>").text(value));
      table.append(row);
    };
    const breakdown = score === null ? [] : score.breakdown || [];
    view.breakdown.forEach(function (row) {
      const computed = breakdown.find(function (candidate) { return candidate.id === row.id; });
      addRow(row.name, computed ? GameView.formatNumber(computed.value) : "");
    });
    addRow("Score", score === null ? "" : GameView.formatNumber(score.score), "score-summary-total");
    // Ranking points only mean something in a qualification match.
    if (lastMatchType === matchTypeQualification) {
      view.rankingPoints.forEach(function (rankingPoint) {
        const value = score === null ? undefined : (score.rp || {})[rankingPoint.id];
        addRow(rankingPoint.name, value === undefined ? "" : GameView.formatNumber(value));
      });
      addRow("Ranking Points", score === null ? "" : GameView.formatNumber(score.rpTotal), "score-summary-total");
    }
    const card = $("<div class='card card-body mb-2'></div>").addClass("bg-" + slot);
    card.append($("<h6></h6>").text(alliance.name + " Score Summary"), table);
    container.append($("<div class='col-lg-6'></div>").append(card));
  });
};

// Fills in everything on the page that the game names, and draws again what depends on it.
const renderGame = function () {
  GameView.alliances().forEach(function (alliance, index) {
    $(`[data-alliance-label=${index}]`).text(alliance.name + " Teams");
  });
  if (lastMatchTime !== null) {
    handleMatchTime(lastMatchTime);
  }
  if (lastScoringStatus !== null) {
    handleScoringStatus(lastScoringStatus);
  }
  renderScoreSummary();
};

// Handles a websocket message to populate the final score data.
const handleScorePosted = function (data) {
  let matchName = data.match ? data.match.LongName : "";
  if (matchName) {
    $("#scoreRadio").prop("disabled", false);
  } else {
    matchName = "None"
  }
  $("#savedMatchName").html(matchName);
}

// Handles a websocket message to update the audience display screen selector.
const handleAudienceDisplayMode = function (data) {
  $("input[name=audienceDisplay]:checked").prop("checked", false);
  $("input[name=audienceDisplay][value=" + data + "]").prop("checked", true);
};

// Handles a websocket message to signal whether the referee and scorers have committed after the match.
const handleScoringStatus = function (data) {
  scoreIsReady = data.RefereeScoreReady;
  for (const status of Object.values(data.PositionStatuses)) {
    if (!status.Ready) {
      scoreIsReady = false;
      break;
    }
  }
  lastScoringStatus = data;
  $("#refereeScoreStatus").attr("data-ready", data.RefereeScoreReady);

  // One badge per scoring panel position of the game, under the name the game gives the panel.
  const names = {};
  if (GameView.isReady()) {
    GameView.view.panels.forEach(function (panel) {
      names[panel.id] = panel.name;
    });
    GameView.alliances().forEach(function (alliance) {
      names[alliance.id] = names[alliance.id] || alliance.name;
    });
  }
  const container = $("#panelScoreStatuses").empty();
  Object.keys(data.PositionStatuses).sort().forEach(function (position) {
    const status = data.PositionStatuses[position];
    const badge = $("<span class='badge badge-scoring'></span>")
      .text(`${names[position] || position} ${status.NumPanelsReady}/${status.NumPanels}`)
      .attr("data-present", status.NumPanels > 0).attr("data-ready", status.Ready);
    container.append("<br/>", badge);
  });
};

// Handles a websocket message to update the alliance station display screen selector.
const handleAllianceStationDisplayMode = function (data) {
  $("input[name=allianceStationDisplay]:checked").prop("checked", false);
  $("input[name=allianceStationDisplay][value=" + data + "]").prop("checked", true);
};

// Handles a websocket message to update the event status message.
const handleEventStatus = function (data) {
  if (data.CycleTime === "") {
    $("#cycleTimeMessage").text("Last cycle time: Unknown");
  } else {
    $("#cycleTimeMessage").text("Last cycle time: " + data.CycleTime);
  }
  $("#earlyLateMessage").text(data.EarlyLateMessage);
};

const formatPlayoffAllianceInfo = function (allianceNumber, offFieldTeams) {
  if (allianceNumber === 0) {
    return "";
  }
  let allianceInfo = `<b>Alliance ${allianceNumber}</b>`;
  if (offFieldTeams.length > 0) {
    allianceInfo += ` (not on field: ${offFieldTeams.map(team => team.Id).join(", ")})`;
  }
  return allianceInfo;
}

$(function () {
  // Activate tooltips above the status headers.
  const tooltipTriggerList = document.querySelectorAll("[data-bs-toggle=tooltip]");
  const tooltipList = [...tooltipTriggerList].map(element => new bootstrap.Tooltip(element));

  // Draw what the game names when it arrives, and again whenever it changes.
  GameView.onChange(renderGame);

  // Set up the websocket back to the server.
  websocket = new CheesyWebsocket("/match_play/websocket", {
    allianceStationDisplayMode: function (event) {
      handleAllianceStationDisplayMode(event.data);
    },
    arenaStatus: function (event) {
      handleArenaStatus(event.data);
    },
    audienceDisplayMode: function (event) {
      handleAudienceDisplayMode(event.data);
    },
    eventStatus: function (event) {
      handleEventStatus(event.data);
    },
    matchLoad: function (event) {
      handleMatchLoad(event.data);
    },
    matchTime: function (event) {
      handleMatchTime(event.data);
    },
    game: function (event) {
      handleGame(event.data);
    },
    realtimeScore: function (event) {
      handleRealtimeScore(event.data);
    },
    scorePosted: function (event) {
      handleScorePosted(event.data);
    },
    scoringStatus: function (event) {
      handleScoringStatus(event.data);
    },
  });
});
