// Copyright 2018 Team 254. All Rights Reserved.
// Author: pat@patfairbank.com (Patrick Fairbank)
//
// Shared client-side logic for interpreting match state and timing notifications from the server.
//
// The phases of a match come from the active game (see game_view.js, which must be loaded first). This file turns
// the "matchTime" websocket message into what displays show: a state name, a label, and a countdown.

const matchTypePractice = 1;
const matchTypeQualification = 2;
const matchTypePlayoff = 3;

// Values of field.MatchState. While IN_MATCH, the message's PhaseIndex says which phase of the game is running.
const matchStates = {
  0: "PRE_MATCH",
  1: "START_MATCH",
  2: "IN_MATCH",
  3: "POST_MATCH",
  4: "TIMEOUT_ACTIVE",
  5: "POST_TIMEOUT"
};

// Returns the stretch of the match that contains the given phase: the run of consecutive phases in which robots
// are in the same state (all autonomous, all teleoperated, or all disabled). Clocks count down to the end of the
// stretch, so a game whose teleop is divided into several phases still shows one teleop countdown.
const getStretch = function (phaseIndex) {
  const phases = GameView.phases();
  const phase = phases[phaseIndex];
  if (!phase) {
    return null;
  }
  let first = phaseIndex;
  while (first > 0 && phases[first - 1].robots === phase.robots) {
    first--;
  }
  let last = phaseIndex;
  while (last < phases.length - 1 && phases[last + 1].robots === phase.robots) {
    last++;
  }
  return {
    robots: phase.robots,
    start: phases[first].start,
    end: phases[last].start + phases[last].duration,
    firstIndex: first,
    lastIndex: last,
  };
};

// Returns the label for a robot state.
const getRobotsText = function (robots) {
  switch (robots) {
    case "auto":
      return "AUTONOMOUS";
    case "teleop":
      return "TELEOPERATED";
    default:
      return "PAUSE";
  }
};

// Returns the phase named by a matchTime message, or null when no phase is running.
const getCurrentPhase = function (data) {
  if (matchStates[data.MatchState] !== "IN_MATCH") {
    return null;
  }
  return GameView.phaseAt(data.PhaseIndex);
};

// Returns the countdown, in whole seconds, that a clock should show for a matchTime message.
//   Before the match: the length of the first stretch.
//   During a stretch in which robots are enabled: the time left in that stretch.
//   During a pause: zero.
//   During a timeout: the time left in the timeout.
const getCountdown = function (data) {
  switch (matchStates[data.MatchState]) {
    case "PRE_MATCH":
    case "START_MATCH": {
      const stretch = getStretch(0);
      return stretch === null ? 0 : Math.round(stretch.end - stretch.start);
    }
    case "IN_MATCH": {
      const stretch = getStretch(data.PhaseIndex);
      if (stretch === null || stretch.robots === "disabled") {
        return 0;
      }
      return Math.max(0, Math.ceil(stretch.end - data.MatchTimeSec));
    }
    case "TIMEOUT_ACTIVE":
      return Math.max(0, Math.ceil(data.TimeoutDurationSec - data.MatchTimeSec));
    default:
      return 0;
  }
};

// Converts the raw match state and time into a human-readable state and countdown. Calls the provided callback with
// the state name (a value of matchStates), the label to show, the countdown in seconds, and the current phase (or
// null when no phase is running). Does nothing until the game has arrived.
const translateMatchTime = function (data, callback) {
  if (!GameView.isReady()) {
    return;
  }
  const matchState = matchStates[data.MatchState];
  const phase = getCurrentPhase(data);
  let matchStateText;
  switch (matchState) {
    case "PRE_MATCH":
      matchStateText = "PRE-MATCH";
      break;
    case "START_MATCH": {
      const first = GameView.phaseAt(0);
      matchStateText = first === null ? "" : getRobotsText(first.robots);
      break;
    }
    case "IN_MATCH":
      matchStateText = phase === null ? "" : getRobotsText(phase.robots);
      break;
    case "POST_MATCH":
      matchStateText = "POST-MATCH";
      break;
    case "TIMEOUT_ACTIVE":
    case "POST_TIMEOUT":
      matchStateText = "TIMEOUT";
      break;
  }
  callback(matchState, matchStateText, getCountdown(data), phase);
};

// Converts the given countdown in seconds to a string with a colon separator and leading zero padding.
const getCountdownString = function (countdownSec) {
  let countdownString = String(countdownSec % 60);
  if (countdownString.length === 1) {
    countdownString = "0" + countdownString;
  }
  return Math.floor(countdownSec / 60) + ":" + countdownString;
};
