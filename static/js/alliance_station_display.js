// Copyright 2014 Team 254. All Rights Reserved.
// Author: pat@patfairbank.com (Patrick Fairbank)
//
// Client-side methods for the alliance station display.
//
// The stations are the field's: R1 to R3 belong to the first alliance of the game and B1 to B3 to the second,
// whatever the game calls them. Phases, alliance colours and scores come from the game view and the generic messages;
// see game_view.js and display_shared.js, which must be loaded first.

var station = "";
var blinkInterval;
var currentScreen = "blank";
var websocket;

// The last message of each kind, kept so that the display can be drawn again when the game arrives or changes.
var lastMatchTime = null;
var lastRealtimeScore = null;

// Handles a websocket message to change which screen is displayed.
var handleAllianceStationDisplayMode = function (targetScreen) {
  currentScreen = targetScreen;
  if (station === "") {
    // Don't do anything if this screen hasn't been assigned a position yet.
  } else {
    var body = $("body");
    body.attr("data-mode", targetScreen);
    if (targetScreen === "timeout") {
      body.attr("data-position", "middle");
    } else {
      switch (station[1]) {
        case "1":
          body.attr("data-position", "right");
          break;
        case "2":
          body.attr("data-position", "middle");
          break;
        case "3":
          body.attr("data-position", "left");
          break;
      }
    }
  }
};

// Handles a websocket message to update the team to display.
var handleMatchLoad = function (data) {
  if (station !== "") {
    var team = data.Teams[station];
    if (team) {
      $("#teamNumber").text(team.Id);
      $("#teamNameText").attr("data-alliance-bg", station[0]).text(team.Nickname);

      var ranking = data.Rankings[team.Id];
      if (ranking && data.Match.Type === matchTypeQualification) {
        $("#teamRank").attr("data-alliance-bg", station[0]).text(ranking);
      } else {
        $("#teamRank").attr("data-alliance-bg", station[0]).text("");
      }
    } else {
      $("#teamNumber").text("");
      $("#teamNameText").attr("data-alliance-bg", station[0]).text("");
      $("#teamRank").attr("data-alliance-bg", station[0]).text("");
    }

    // Populate extra alliance info if this is a playoff match.
    let playoffAlliance = data.Match.PlayoffRedAlliance;
    let offFieldTeams = data.RedOffFieldTeams;
    if (station[0] === "B") {
      playoffAlliance = data.Match.PlayoffBlueAlliance;
      offFieldTeams = data.BlueOffFieldTeams;
    }
    if (playoffAlliance > 0) {
      let playoffAllianceInfo = `Alliance ${playoffAlliance}`;
      if (offFieldTeams.length) {
        playoffAllianceInfo += `&emsp; Not on field: ${offFieldTeams.map(team => team.Id).join(", ")}`;
      }
      $("#playoffAllianceInfo").html(playoffAllianceInfo);
    } else {
      $("#playoffAllianceInfo").text("");
    }
  }
};

// Handles a websocket message to update the team connection status.
var handleArenaStatus = function (data) {
  stationStatus = data.AllianceStations[station];
  var blink = false;
  if (stationStatus && stationStatus.Bypass) {
    $("#match").attr("data-status", "bypass");
  } else if (stationStatus) {
    if (!stationStatus.DsConn || !stationStatus.DsConn.DsLinked) {
      $("#match").attr("data-status", station[0]);
    } else if (!stationStatus.DsConn.RobotLinked) {
      blink = true;
      if (!blinkInterval) {
        blinkInterval = setInterval(function () {
          var status = $("#match").attr("data-status");
          $("#match").attr("data-status", (status === "") ? station[0] : "");
        }, 250);
      }
    } else {
      $("#match").attr("data-status", "");
    }
  }

  if (!blink && blinkInterval) {
    clearInterval(blinkInterval);
    blinkInterval = null;
  }
};

// Handles a websocket message to update the match time countdown.
var handleMatchTime = function (data) {
  lastMatchTime = data;
  translateMatchTime(data, function (matchState, matchStateText, countdownSec, phase) {
    if (station[0] === "N") {
      // Pin the state for a non-alliance display to an in-match state, so as to always show time or score.
      matchState = "IN_MATCH";
    }
    $("#timeRemaining").text(getCountdownString(countdownSec));
    $("#phaseName").text(phase === null ? "" : phase.name);
    $("#match").attr("data-state", matchState);
  });
};

// Handles a websocket message to update the match score.
var handleRealtimeScore = function (data) {
  lastRealtimeScore = data;
  $("#redScore").text(DisplayShared.scoreText(data, GameView.allianceIdForSlot("red")));
  $("#blueScore").text(DisplayShared.scoreText(data, GameView.allianceIdForSlot("blue")));
};

$(function () {
  // Read the configuration for this display from the URL query string.
  var urlParams = new URLSearchParams(window.location.search);
  station = urlParams.get("station");

  // Draw the clock and the scores again when the game arrives or changes.
  GameView.onChange(function () {
    if (lastMatchTime !== null) {
      handleMatchTime(lastMatchTime);
    }
    if (lastRealtimeScore !== null) {
      handleRealtimeScore(lastRealtimeScore);
    }
  });

  // Set up the websocket back to the server.
  websocket = new CheesyWebsocket("/displays/alliance_station/websocket", {
    allianceStationDisplayMode: function (event) {
      handleAllianceStationDisplayMode(event.data);
    },
    arenaStatus: function (event) {
      handleArenaStatus(event.data);
    },
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
    }
  });
});
