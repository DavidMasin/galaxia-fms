// Copyright 2026 Team 254. All Rights Reserved.

// Client-side logic for the FMS field monitor display.
//
// The stations are the field's: R1 to R3 belong to the first alliance of the game and B1 to B3 to the second,
// whatever the game calls them. Phases, alliance names and colours and scores come from the game view and the generic
// messages; see game_view.js and display_shared.js, which must be loaded first.

let websocket;
let currentMatchId;
let redSide;
let blueSide;
const lowBatteryThreshold = 8;
const highBtuThreshold = 7.0;
const minBatteryTracker = {};
const disconnectTracker = { radio: {}, rio: {} };

// Where the match stands, from the last matchTime message: a value of matchStates, and what the robots are being told
// ("auto", "teleop", "disabled", or "" when no phase is running).
let currentMatchState = null;
let currentRobots = "";

// The last message of each kind, kept so that the display can be drawn again when the game arrives or changes.
let lastMatchTime = null;
let lastRealtimeScore = null;

function formatDisconnectTimer(ms) {
  const s = Math.floor(ms / 1000);
  return s < 60 ? `${s}s` : `${Math.floor(s / 60)}m ${s % 60}s`;
}

function getDisconnectText(type, station, inMatch, baseText) {
  if (inMatch) {
    if (!disconnectTracker[type][station]) disconnectTracker[type][station] = Date.now();
    return baseText + " (" + formatDisconnectTimer(Date.now() - disconnectTracker[type][station]) + ")";
  } else {
    disconnectTracker[type][station] = null;
    return baseText;
  }
}

const handleArenaStatus = function (data) {
  if (currentMatchId == null) {
    currentMatchId = data.MatchId;
  } else if (currentMatchId !== data.MatchId) {
    location.reload();
  }

  const inMatch = currentMatchState === "START_MATCH" || currentMatchState === "IN_MATCH";

  $.each(data.AllianceStations, function (station, stationStatus) {
    let teamElementPrefix;
    if (station[0] === "R") {
      teamElementPrefix = "#" + redSide + "Team" + station[1];
    } else {
      teamElementPrefix = "#" + blueSide + "Team" + station[1];
    }

    const teamIdElement = $(teamElementPrefix + "Id");
    const teamNotesElement = $(teamElementPrefix + "Notes");
    const teamNotesTextElement = $(teamElementPrefix + "Notes div");
    
    const teamDsElement = $(teamElementPrefix + "Ds");
    const teamDsText = teamDsElement.find(".fms-status-text");
    const teamRadioElement = $(teamElementPrefix + "Radio");
    const teamRadioIconElement = teamRadioElement.find("i");
    const teamRadioText = teamRadioElement.find(".fms-status-text");
    const teamRioElement = $(teamElementPrefix + "Rio");
    const teamRioText = teamRioElement.find(".fms-status-text");
    const teamRioIconElement = teamRioElement.find("i");
    const teamRobotElement = $(teamElementPrefix + "Robot");

    const teamStatsElement = $(teamElementPrefix + "Stats");
    const teamBatteryElement = $(teamElementPrefix + "Battery");
    const teamBatteryParent = teamBatteryElement.parent();
    const teamBandwidthElement = $(teamElementPrefix + "Bandwidth");
    const teamBandwidthParent = teamBandwidthElement.parent();
    const teamTripTimeElement = $(teamElementPrefix + "TripTime");
    const teamTripTimeParent = teamTripTimeElement.parent();
    const teamMissedPacketsElement = $(teamElementPrefix + "MissedPackets");
    const teamMissedPacketsParent = teamMissedPacketsElement.parent();

    teamNotesTextElement.attr("data-station", station);

    const teamNewKeyElement = $(teamElementPrefix).find(".fms-new-key-badge");

    if (stationStatus.Team) {
      teamIdElement.text(stationStatus.Team.Id);
      teamNotesTextElement.text(stationStatus.Team.FtaNotes);
      if (stationStatus.Team.HasConnected) {
        teamNewKeyElement.hide();
      } else {
        teamNewKeyElement.show();
      }
    } else {
      teamIdElement.text("");
      teamNotesTextElement.text("");
      teamNewKeyElement.hide();
    }

    $("#accessPointStatus").attr("data-status", data.AccessPointStatus);
    $("#switchStatus").attr("data-status", data.SwitchStatus);

    const wifiStatus = stationStatus.WifiStatus;
    teamRadioIconElement.attr("class", `bi bi-reception-${wifiStatus.ConnectionQuality}`);

    if (stationStatus.DsConn) {
      const dsConn = stationStatus.DsConn;
      
      // DS Box
      teamDsElement.removeAttr("data-status-ok");
      teamDsElement.removeAttr("data-status-warning");
      if (dsConn.DsLinked) {
        teamDsElement.attr("data-status-ok", true);
        teamDsText.text("DS");
      } else if (dsConn && dsConn.WrongStation) {
        teamDsElement.attr("data-status-warning", true);
        let moveText = "x WRONG DS";
        if (dsConn.WrongStation.length >= 2) {
          const allianceName = DisplayShared.stationAllianceName(dsConn.WrongStation).toUpperCase();
          moveText = "MOVE TO " + allianceName + " " + dsConn.WrongStation[1];
        }
        teamDsText.text(moveText);
      } else if (stationStatus.Ethernet) {
        teamDsElement.attr("data-status-warning", true);
        teamDsText.text("⚠ DS");
      } else {
        teamDsText.text("x DS");
      }

      // Radio Box
      const expectedTeamId = stationStatus.Team ? stationStatus.Team.Id : 0;
      const isRadioAssociated = (wifiStatus.TeamId === expectedTeamId && wifiStatus.RadioLinked);
      const isRadioPingable = dsConn && dsConn.RadioLinked;
      const isRobotLinked = dsConn && dsConn.RobotLinked;

      const radioAssociated = isRadioAssociated || isRobotLinked;
      const radioPingable = isRadioPingable || isRobotLinked;

      teamRadioElement.removeAttr("data-status-ok");
      teamRadioElement.removeAttr("data-status-warning");

      if (wifiStatus.TeamId !== expectedTeamId && wifiStatus.TeamId !== 0) {
        teamRadioElement.attr("data-status-ok", false);
        teamRadioText.text(getDisconnectText("radio", station, inMatch, "x RADIO"));
        teamRadioIconElement.attr("class", "bi bi-reception-4 slashed-icon");
      } else if (radioAssociated && radioPingable) {
        disconnectTracker.radio[station] = null;
        teamRadioElement.attr("data-status-ok", true);
        teamRadioText.text("RADIO");
        
        let radioIcon = "bi-reception-4";
        if (wifiStatus.RadioLinked) {
          radioIcon = "bi-reception-" + (wifiStatus.ConnectionQuality > 0 ? wifiStatus.ConnectionQuality : 1);
        }
        teamRadioIconElement.attr("class", "bi " + radioIcon);
      } else if (radioAssociated && !radioPingable) {
        teamRadioElement.attr("data-status-warning", true);
        teamRadioText.text(getDisconnectText("radio", station, inMatch, "⚠ RADIO"));
        teamRadioIconElement.attr("class", "bi bi-laptop slashed-icon");
      } else {
        teamRadioText.text(getDisconnectText("radio", station, inMatch, "x RADIO"));
        teamRadioIconElement.attr("class", "bi bi-reception-4 slashed-icon");
      }

      // RIO Box
      teamRioElement.removeAttr("data-status-ok");
      teamRioElement.removeAttr("data-status-warning");

      if (dsConn.RobotLinked) {
        disconnectTracker.rio[station] = null;
        teamRioElement.attr("data-status-ok", true);
        teamRioText.text("RIO");
        teamRioIconElement.attr("class", "bi bi-cpu");
      } else if (dsConn.RioLinked) {
        teamRioElement.attr("data-status-warning", true);
        teamRioText.text(getDisconnectText("rio", station, inMatch, "⚠ RIO"));
        teamRioIconElement.attr("class", "bi bi-cpu-fill");
      } else {
        teamRioElement.attr("data-status-ok", false);
        teamRioText.text(getDisconnectText("rio", station, inMatch, "x RIO"));
        teamRioIconElement.attr("class", "bi bi-cpu");
      }

      // Stats
      teamStatsElement.removeAttr("data-status-ok");

      let minBat = minBatteryTracker[station] || 99.9;
      
      if (dsConn.RobotLinked && dsConn.BatteryVoltage > 0) {
        if (dsConn.BatteryVoltage < minBat) {
          minBat = dsConn.BatteryVoltage;
          minBatteryTracker[station] = minBat;
        }
      } else if (!dsConn.RobotLinked && currentMatchState === "PRE_MATCH") {
        minBatteryTracker[station] = null;
        minBat = 0.0;
      }
      
      let minBatText = (minBat === 99.9 || minBat === 0.0) ? "0.0" : minBat.toFixed(1);

      teamBatteryElement.html(dsConn.BatteryVoltage.toFixed(1) + 'V <span class="fms-stat-sub">Min ' + minBatText + '</span>');
      if (dsConn.RobotLinked && dsConn.BatteryVoltage < lowBatteryThreshold) {
        teamBatteryParent.attr("data-status-ok", false);
      } else {
        teamBatteryParent.removeAttr("data-status-ok");
      }

      if (wifiStatus.MBits >= 0.01) {
        teamBandwidthElement.text(wifiStatus.MBits.toFixed(3) + " Mbps");
        if (dsConn.RobotLinked && wifiStatus.MBits >= highBtuThreshold) {
          teamBandwidthParent.attr("data-status-ok", false);
        } else {
          teamBandwidthParent.removeAttr("data-status-ok");
        }
        teamTripTimeElement.text(dsConn.DsRobotTripTimeMs + " ms");
        teamMissedPacketsElement.text(dsConn.MissedPacketCount);
      } else {
        teamBandwidthElement.text("0.000 Mbps");
        teamBandwidthParent.removeAttr("data-status-ok");
        teamTripTimeElement.text("0 ms");
        teamMissedPacketsElement.text("0");
      }
      
      teamTripTimeParent.removeAttr("data-status-ok");
      teamMissedPacketsParent.removeAttr("data-status-ok");
    } else {
      teamDsElement.removeAttr("data-status-ok");
      teamDsElement.removeAttr("data-status-warning");
      if (stationStatus.Ethernet) {
        teamDsElement.attr("data-status-warning", true);
        teamDsText.text("⚠ DS");
      } else {
        teamDsText.text("x DS");
      }

      teamRioElement.attr("data-status-ok", "");
      teamRioText.text("x RIO");
      
      teamStatsElement.removeAttr("data-status-ok");
      
      if (currentMatchState === "PRE_MATCH") {
        minBatteryTracker[station] = null;
      }
      let minBat = minBatteryTracker[station];
      let minBatText = minBat ? minBat.toFixed(1) : "0.0";
      
      teamBatteryElement.html('0.0V <span class="fms-stat-sub">Min ' + minBatText + '</span>');
      teamBatteryParent.removeAttr("data-status-ok");
      
      teamBandwidthElement.text("0.000 Mbps");
      teamBandwidthParent.removeAttr("data-status-ok");
      
      teamTripTimeElement.text("0 ms");
      teamTripTimeParent.removeAttr("data-status-ok");
      
      teamMissedPacketsElement.text("0");
      teamMissedPacketsParent.removeAttr("data-status-ok");

      teamRadioElement.removeAttr("data-status-ok");
      teamRadioElement.removeAttr("data-status-warning");
      const expectedTeamId = stationStatus.Team ? stationStatus.Team.Id : 0;
      if (wifiStatus.TeamId !== expectedTeamId && wifiStatus.TeamId !== 0) {
        teamRadioElement.attr("data-status-ok", false);
        teamRadioText.text(getDisconnectText("radio", station, inMatch, "x RADIO"));
        teamRadioIconElement.attr("class", "bi bi-reception-4 slashed-icon");
      } else if (wifiStatus.TeamId === expectedTeamId && wifiStatus.RadioLinked) {
        teamRadioElement.attr("data-status-warning", true);
        teamRadioText.text(getDisconnectText("radio", station, inMatch, "⚠ RADIO"));
        teamRadioIconElement.attr("class", "bi bi-laptop slashed-icon");
      } else {
        teamRadioText.text(getDisconnectText("radio", station, inMatch, "x RADIO"));
        teamRadioIconElement.attr("class", "bi bi-reception-4 slashed-icon");
      }

      teamRioElement.removeAttr("data-status-ok");
      teamRioElement.removeAttr("data-status-warning");
      teamRioElement.attr("data-status-ok", false);
      teamRioText.text(getDisconnectText("rio", station, inMatch, "x RIO"));
      teamRioIconElement.attr("class", "bi bi-cpu");
    }

    // Robot state (E-Stop, Bypass, etc.)
    const teamElement = $(teamElementPrefix);
    
    teamRobotElement.removeAttr("data-status-ok");
    
    if (stationStatus.EStop) {
      teamElement.attr("data-bypassed", "false");
      teamRobotElement.attr("data-robot-state", "estop");
      teamRobotElement.text("E-STOP");
    } else if (stationStatus.AStop) {
      teamElement.attr("data-bypassed", "false");
      teamRobotElement.attr("data-robot-state", "astop");
      teamRobotElement.text("A-STOP");
    } else if (stationStatus.Bypass) {
      teamElement.attr("data-bypassed", "true");
      teamRobotElement.attr("data-robot-state", "bypassed");
      teamRobotElement.text("BYPASSED");
    } else if (!stationStatus.Team) {
      teamElement.attr("data-bypassed", "false");
      teamRobotElement.attr("data-robot-state", "no-team");
      teamRobotElement.text("NO TEAM");
    } else if (inMatch) {
      teamElement.attr("data-bypassed", "false");
      const ds = stationStatus.DsConn;
      
      let isAuto = false;
      let isEnabled = false;

      if (ds) {
        isAuto = ds.Auto;
        isEnabled = ds.Enabled;
      } else {
        isAuto = currentRobots === "auto";
        isEnabled = false;
      }

      if (isAuto && isEnabled) {
        teamRobotElement.attr("data-robot-state", "auto");
        teamRobotElement.text("AUTO");
      } else if (isAuto && !isEnabled) {
        teamRobotElement.attr("data-robot-state", "auto-disabled");
        teamRobotElement.text("AUTO DISABLED");
      } else if (!isAuto && isEnabled) {
        teamRobotElement.attr("data-robot-state", "teleop");
        teamRobotElement.text("TELEOP");
      } else {
        teamRobotElement.attr("data-robot-state", "teleop-disabled");
        teamRobotElement.text("TELEOP DISABLED");
      }
    } else if (!stationStatus.DsConn || !stationStatus.DsConn.RobotLinked) {
      teamElement.attr("data-bypassed", "false");
      teamRobotElement.attr("data-robot-state", "not-ready");
      teamRobotElement.text("NOT READY");
    } else {
      teamElement.attr("data-bypassed", "false");
      teamRobotElement.attr("data-robot-state", "ready");
      teamRobotElement.text("READY");
    }

    const isFaded = stationStatus.Bypass || stationStatus.EStop || currentMatchState === "POST_MATCH";
    teamElement.attr("data-faded", isFaded ? "true" : "false");
  });
};

const handleMatchTime = function (data) {
  lastMatchTime = data;
  const clock = DisplayShared.clock(data);
  if (clock === null) {
    return;
  }
  if (currentMatchState === "PRE_MATCH" && (clock.state === "START_MATCH" || clock.state === "IN_MATCH")) {
    // A match is starting: forget the lowest battery voltages seen before it.
    for (const stationId in minBatteryTracker) {
      minBatteryTracker[stationId] = null;
    }
  }
  currentMatchState = clock.state;
  currentRobots = clock.robots;

  $("#matchState").text(clock.stateLabel);
  $("#matchTime").text(clock.countdownSec);
  if (clock.state === "PRE_MATCH" || clock.state === "POST_MATCH") {
    $(".ds-dependent").attr("data-preMatch", "true");
  } else {
    $(".ds-dependent").attr("data-preMatch", "false");
  }
};

const handleRealtimeScore = function (data) {
  lastRealtimeScore = data;
  $(`#${redSide}Score`).text(DisplayShared.scoreText(data, GameView.allianceIdForSlot("red")));
  $(`#${blueSide}Score`).text(DisplayShared.scoreText(data, GameView.allianceIdForSlot("blue")));
};

const handleMatchLoad = function (data) {
  $("#matchName").text(data.Match.LongName);
};

const handleEventStatus = function (data) {
  if (data.CycleTime === "") {
    $("#cycleTimeMessage").text("Last cycle time: Unknown");
  } else {
    $("#cycleTimeMessage").text("Last cycle time: " + data.CycleTime);
  }
  $("#earlyLateMessage").text(data.EarlyLateMessage);
};

const editFtaNotes = function (element) {
  const teamNotesTextElement = $(element);
  const textArea = $("<textarea />");
  textArea.val(teamNotesTextElement.text());
  teamNotesTextElement.replaceWith(textArea);
  textArea.focus();
  textArea.blur(function () {
    textArea.replaceWith(teamNotesTextElement);
    if (textArea.val() !== teamNotesTextElement.text()) {
      websocket.send("updateTeamNotes", {station: teamNotesTextElement.attr("data-station"), notes: textArea.val()});
    }
  });
};

$(function () {
  $(document).keydown(function(e) {
    if ((e.ctrlKey || e.metaKey) && e.key.toLowerCase() === 'b') {
      e.preventDefault();
      const currentUrl = new URL(window.location.href);
      const isReversed = currentUrl.searchParams.get("reversed") === "true";
      currentUrl.searchParams.set("reversed", (!isReversed).toString());
      window.location.href = currentUrl.toString();
    }
  });

  const urlParams = new URLSearchParams(window.location.search);
  const reversed = urlParams.get("reversed") === "true";
  if (reversed) {
    redSide = "right";
    blueSide = "left";
  } else {
    redSide = "left";
    blueSide = "right";
  }

  const driverStation = urlParams.get("ds");
  if (driverStation === "true") {
    $(".fta-dependent").attr("data-fta", "false");
    $(".ds-dependent").attr("data-ds", driverStation);
  } else {
    $(".fta-dependent").attr("data-fta", urlParams.get("fta"));
    $(".ds-dependent").attr("data-ds", driverStation);
  }

  $(".reversible-left").attr("data-reversed", reversed ? "true" : "false");
  $(".reversible-right").attr("data-reversed", reversed ? "true" : "false");

  // Draw the clock and the scores again when the game arrives or changes.
  GameView.onChange(function () {
    if (lastMatchTime !== null) {
      handleMatchTime(lastMatchTime);
    }
    if (lastRealtimeScore !== null) {
      handleRealtimeScore(lastRealtimeScore);
    }
  });

  websocket = new CheesyWebsocket("/displays/fms_field_monitor/websocket", {
    arenaStatus: function (event) {
      handleArenaStatus(event.data);
    },
    eventStatus: function (event) {
      handleEventStatus(event.data);
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
    },
  });
});
