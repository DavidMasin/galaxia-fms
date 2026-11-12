// Copyright 2018 Team 254. All Rights Reserved.
// Author: pat@patfairbank.com (Patrick Fairbank)
//
// Client-side logic for the queueing display.

var websocket;

// The last matchTime message, kept so that the clock can be drawn again when the game arrives or changes and when the
// list of matches is replaced.
var lastMatchTime = null;

// Handles a websocket message to update the teams for the current match.
var handleMatchLoad = function (data) {
  fetch("/displays/queueing/match_load")
    .then(response => response.text())
    .then(html => {
      $("#matches").html(html);
      if (lastMatchTime !== null) {
        // The clock of the match on the field is part of the list that was just replaced.
        handleMatchTime(lastMatchTime);
      }
    });
};

// Handles a websocket message to update the match time countdown.
var handleMatchTime = function (data) {
  lastMatchTime = data;
  var clock = DisplayShared.clock(data);
  if (clock === null) {
    return;
  }
  $("#matchState").text(clock.stateLabel);
  $("#matchTime").text(clock.countdown);
};

// Handles a websocket message to update the event status message.
var handleEventStatus = function (data) {
  $("#earlyLateMessage").text(data.EarlyLateMessage);
};

$(function () {
  // Draw the clock again when the game arrives or changes.
  GameView.onChange(function () {
    if (lastMatchTime !== null) {
      handleMatchTime(lastMatchTime);
    }
  });

  // Set up the websocket back to the server.
  websocket = new CheesyWebsocket("/displays/queueing/websocket", {
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
  });
});
