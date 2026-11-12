// Copyright 2014 Team 254. All Rights Reserved.
// Author: nick@team254.com (Nick Eyre)
// Author: pat@patfairbank.com (Patrick Fairbank)
//
// Client-side methods for the rankings display.
//
// The columns between the team's name and its ranking points are the keys the active game ranks teams by. The server
// lists them with the rankings, and the table is built from that list; see display_shared.js, which must be loaded
// first.

var websocket;
var initialDwellMs = 3000;  // How long the display waits upon initial load before scrolling.
var scrollMsPerRow;  // How long in milliseconds it takes to scroll a height of one row.
var staticUpdateIntervalMs = 10000;  // How long between updates if not scrolling.
var rankingsData;
var prevHighestPlayedMatch;
var prevColumns;

// Loads the JSON rankings data from the event server.
var getRankingsData = function (callback) {
  $.getJSON("/api/rankings", function (data) {
    rankingsData = data;
    if (callback) {
      callback(data);
    }
  });
};

// Sets the headings of the table, and the width of its columns, for the given columns of the game's ranking order.
var setColumns = function (columns) {
  $("#header").html(DisplayShared.rankingsHeaderHtml(columns));
  document.getElementById("standings").style.setProperty(
    "--ranking-field-width", DisplayShared.rankingsFieldWidth(columns) + "%"
  );
};

// Updates the rankings in place and initiates scrolling if they are long enough to require it.
var updateStaticRankings = function () {
  getRankingsData(function () {
    var rankingsHtml = DisplayShared.rankingsRowsHtml(rankingsData);
    $("#rankings2").html(rankingsHtml);
    $("#scroller").css("transform", "translate(0px, -2px);");
    prevHighestPlayedMatch = rankingsData.HighestPlayedMatch;
    setHighestPlayedMatch(rankingsData.HighestPlayedMatch);
    prevColumns = rankingsData.Columns;
    setColumns(rankingsData.Columns);
    if ($("#rankings2").height() > $("#container").height()) {
      // Initiate scrolling.
      setTimeout(cycleRankings, initialDwellMs);
    } else {
      // Rankings are too short; just update in place.
      setTimeout(updateStaticRankings, staticUpdateIntervalMs);
    }
  });
};

// Seamlessly copies the newer table contents to the older one, resets the scrolling, and loads new data.
var cycleRankings = function () {
  // Overwrite the top data with the bottom data and reset the scrolling back up to the top of the top table.
  $("#rankings1").html($("#rankings2").html());
  $("#scroller").css({transform: "translate(0px, -1px);"});

  // Load new data into the now out-of-sight bottom table.
  var rankingsHtml = DisplayShared.rankingsRowsHtml(rankingsData);
  $("#rankings2").html(rankingsHtml);

  // Delay updating the "Standings as of" message and the column headings by one cycle because the tables are always
  // one cycle behind the data loading.
  setHighestPlayedMatch(prevHighestPlayedMatch);
  prevHighestPlayedMatch = rankingsData.HighestPlayedMatch;
  setColumns(prevColumns);
  prevColumns = rankingsData.Columns;

  if ($("#rankings1").height() > $("#container").height()) {
    // Kick off another scrolling animation.
    var scrollDistance = $("#rankings1").height() + parseInt($("#rankings1").css("border-bottom-width"));
    var scrollTime = scrollMsPerRow * $("#rankings1 tr").length;
    $("#scroller").transition({y: -scrollDistance}, scrollTime, "linear", cycleRankings);

    // Set the data to be reloaded two seconds before the scrolling terminates.
    var reloadDataTime = Math.max(0, scrollTime - 2000);
    setTimeout(getRankingsData, reloadDataTime);
  } else {
    // The rankings got shorter for whatever reason, so revert to static updating.
    setTimeout(updateStaticRankings, staticUpdateIntervalMs);
  }
};

// Updates the "Standings as of" message with the given value, or blanks it out if there is no data yet.
var setHighestPlayedMatch = function (highestPlayedMatch) {
  if (highestPlayedMatch === "") {
    $("#highestPlayedMatch").text("");
  } else {
    $("#highestPlayedMatch").text("Standings as of " + highestPlayedMatch);
  }
};

// Handles a websocket message to update the event status message.
var handleEventStatus = function (data) {
  $("#earlyLateMessage").text(data.EarlyLateMessage);
};

$(function () {
  // Read the configuration for this display from the URL query string.
  var urlParams = new URLSearchParams(window.location.search);
  scrollMsPerRow = urlParams.get("scrollMsPerRow");

  // Set up the websocket back to the server. Used only for remote forcing of reloads.
  websocket = new CheesyWebsocket("/displays/rankings/websocket", {
    eventStatus: function (event) {
      handleEventStatus(event.data);
    },
  });

  updateStaticRankings();
});
