// Copyright 2026 Team 254. All Rights Reserved.
// Author: pat@patfairbank.com (Patrick Fairbank)
//
// Shared helpers for the display pages.
//
// Everything a display shows about the game comes from the game view (game_view.js) and from the generic "matchTime",
// "realtimeScore" and "scorePosted" websocket messages. Load game_view.js and match_timing.js before this file.
//
// The functions under "Pure helpers" return plain data or HTML strings and touch neither the page nor the network, so
// they can be exercised outside a browser. The ones under "Overlay" apply them to the markup that the audience and
// wall displays share.

(function (window) {
  // The match record, the field's stations and the two-sided layouts have two slots, kept from two-alliance games:
  // the first alliance of the game occupies "red" and the second "blue", whatever the game calls and colours them.
  const fieldSlots = [
    {slot: "red", matchPrefix: "Red", stationPrefix: "R"},
    {slot: "blue", matchPrefix: "Blue", stationPrefix: "B"},
  ];

  // The overlay has room for this many teams on each side.
  const overlayTeamRows = 3;

  // Sizes of the items shown beside the score, in pixels. They match display_overlay_shared.css.
  const liveItemWidth = 80;
  const indicatorColumnWidth = 100;
  const indicatorsPerColumn = 3;

  // A value longer than this many characters is shown in smaller type.
  const liveValueShortLength = 4;

  // Colour of a card the active game does not define, which happens when the match shown was played with another
  // game.
  const unknownCardColor = "#999";

  // Height available to the rows of the final score, and the tallest and shortest a row may be, in pixels.
  const finalRowsHeight = 360;
  const finalRowMaxHeight = 36;
  const finalRowMinHeight = 16;

  const escapeHtml = function (value) {
    return String(value === null || value === undefined ? "" : value)
      .replace(/&/g, "&amp;")
      .replace(/</g, "&lt;")
      .replace(/>/g, "&gt;")
      .replace(/"/g, "&quot;")
      .replace(/'/g, "&#39;");
  };

  const findById = function (list, id) {
    return (list || []).find(function (item) {
      return item.id === id;
    }) || null;
  };

  const getFieldSlot = function (slot) {
    return fieldSlots[slot === "blue" ? 1 : 0];
  };

  // Returns one alliance's part of a realtimeScore message, or null if there is none.
  const getAllianceScore = function (score, allianceId) {
    if (!score || !score.alliances) {
      return null;
    }
    return score.alliances[allianceId] || null;
  };

  // Describes what an audience item of the game refers to: an element ("<element id>" or "<element id>.total", and
  // for a counter also "<element id>.<phase id>"), or a derived or live value by its id. Returns null if the game has
  // nothing by that name.
  const describeItem = function (ref) {
    if (!GameView.isReady()) {
      return null;
    }
    const dot = ref.indexOf(".");
    const id = dot === -1 ? ref : ref.substring(0, dot);
    const part = dot === -1 ? "" : ref.substring(dot + 1);
    const element = GameView.element(id);
    if (element !== null) {
      const phase = element.type === "counter" ? GameView.phase(part) : null;
      return {
        kind: "element",
        id: id,
        name: phase === null ? element.name : element.name + " " + phase.name,
        scope: element.scope,
        element: element,
        phaseId: phase === null ? "" : phase.id,
      };
    }
    const derived = findById(GameView.view.derived, id);
    if (derived !== null) {
      // A derived value that earns points is reported with the points; any other with the values.
      return {
        kind: derived.points ? "points" : "values", id: id, name: derived.name || id, scope: derived.scope,
      };
    }
    const live = findById(GameView.view.live, id);
    if (live !== null) {
      return {kind: "live", id: id, name: live.name || id, scope: live.scope};
    }
    return null;
  };

  // Returns the raw values of an element as they stand in a realtimeScore message: one for an alliance or field
  // element, one per robot for a robot element.
  const getRawValues = function (score, allianceId, element) {
    if (element.scope === "field") {
      return [((score.field || {}).state || {})[element.id]];
    }
    const alliance = getAllianceScore(score, allianceId);
    const state = alliance === null ? null : alliance.state;
    if (!state) {
      return [];
    }
    if (element.scope === "robot") {
      return (state.robots || []).map(function (robot) {
        return (robot || {})[element.id];
      });
    }
    return [(state.elements || {})[element.id]];
  };

  // Formats the value of an element for a person to read. A robot element is summed over the alliance's robots; a
  // robot boolean is the number of robots for which it is true, and a robot enum lists each robot's option.
  const getElementText = function (item, raws) {
    const element = item.element;
    switch (element.type) {
      case "counter":
        return GameView.formatNumber(raws.reduce(function (sum, raw) {
          if (item.phaseId === "") {
            return sum + GameView.counterTotal(raw);
          }
          return sum + ((raw && raw.counts && raw.counts[item.phaseId]) || 0);
        }, 0));
      case "boolean": {
        const count = raws.filter(function (raw) {
          return GameView.plainValue(element, raw);
        }).length;
        if (element.scope === "robot") {
          return String(count);
        }
        return count > 0 ? "Yes" : "No";
      }
      case "enum":
        return raws.map(function (raw) {
          return GameView.optionName(element, GameView.plainValue(element, raw));
        }).join(" / ");
      default: {
        const total = raws.reduce(function (sum, raw) {
          return sum + GameView.plainValue(element, raw);
        }, 0);
        const unit = element.type === "timer" ? "s" : (element.unit || "");
        return GameView.formatNumber(total) + (unit === "" ? "" : " " + unit);
      }
    }
  };

  // Returns the current value of a derived or live value from a realtimeScore message, or undefined if the message
  // does not have it.
  const getComputedValue = function (score, allianceId, item) {
    if (item.scope === "field") {
      return ((score.field || {})[item.kind] || {})[item.id];
    }
    const alliance = getAllianceScore(score, allianceId);
    if (alliance === null) {
      return undefined;
    }
    return (alliance[item.kind] || {})[item.id];
  };

  const getValueText = function (value) {
    if (value === true) {
      return "Yes";
    }
    if (value === false) {
      return "No";
    }
    return GameView.formatNumber(value);
  };

  // Describes one team on the final score screen.
  const getFinalTeam = function (alliance, teamId, cardId, offField, cards) {
    // The message carries the cards of the game the match was played with; the active game is only a fallback.
    let card = null;
    if (cardId !== "") {
      card = (cards || []).find(function (candidate) {
        return candidate.id === cardId;
      }) || GameView.card(cardId);
    }
    const ranking = (alliance.rankings || {})[teamId];
    let rank = "";
    let rankChange = "";
    if (ranking && ranking.rank !== 0) {
      rank = String(ranking.rank);
      if (ranking.rank > ranking.previousRank && ranking.previousRank > 0) {
        rankChange = "down";
      } else if (ranking.rank < ranking.previousRank) {
        rankChange = "up";
      }
    }
    return {
      teamId: teamId,
      offField: offField,
      card: cardId,
      cardColor: cardId === "" ? "" : (card !== null && card.color ? card.color : unknownCardColor),
      rank: rank,
      rankChange: rankChange,
    };
  };

  // Describes one side of the final score screen from an alliance of a scorePosted message.
  const getFinalSide = function (allianceId, alliance, playoffAlliance, cards) {
    const side = {
      allianceId: allianceId,
      score: "",
      result: "",
      resultLabel: "",
      teams: [],
      playoffAlliance: playoffAlliance,
      wins: 0,
      destination: "",
      won: false,
    };
    if (alliance === null) {
      return side;
    }
    side.score = GameView.formatNumber(alliance.score);
    if (alliance.result === "win") {
      side.result = "winner";
      side.resultLabel = "WINNER";
      side.won = true;
    } else if (alliance.result === "tie") {
      side.result = "tie";
      side.resultLabel = "TIE";
    }
    (alliance.teams || []).forEach(function (teamId, index) {
      if (teamId > 0) {
        // Cards are keyed by robot position, which starts at one.
        side.teams.push(getFinalTeam(alliance, teamId, (alliance.cards || {})[index + 1] || "", false, cards));
      }
    });
    (alliance.offFieldTeams || []).forEach(function (teamId) {
      if (teamId > 0) {
        side.teams.push(getFinalTeam(alliance, teamId, "", true, cards));
      }
    });
    side.wins = alliance.wins || 0;
    side.destination = alliance.destination || "";
    return side;
  };

  // Collects one row of the final score from a list each alliance has under the given property: the entries are
  // matched by id, in the order of the first alliance that has them.
  const collectRows = function (alliances, property) {
    const rows = [];
    alliances.forEach(function (alliance, allianceIndex) {
      ((alliance || {})[property] || []).forEach(function (entry) {
        let row = findById(rows, entry.id);
        if (row === null) {
          row = {id: entry.id, name: entry.name, numbers: alliances.map(function () {
            return null;
          })};
          rows.push(row);
        }
        row.numbers[allianceIndex] = entry.value;
      });
    });
    return rows;
  };

  // Builds the rows of the final score: one per breakdown entry, the total, and then for a qualification match the
  // ranking points that were earned and their total, or for a playoff match the wins in the series.
  const getFinalRows = function (alliances, isQualification, isPlayoff) {
    const formatAll = function (numbers) {
      return numbers.map(function (number) {
        return GameView.formatNumber(number);
      });
    };
    const perAlliance = function (getValue) {
      return alliances.map(function (alliance) {
        return alliance === null ? "" : GameView.formatNumber(getValue(alliance));
      });
    };

    const rows = collectRows(alliances, "breakdown").map(function (row) {
      return {kind: "breakdown", name: row.name, values: formatAll(row.numbers)};
    });
    rows.push({kind: "total", name: "Total", values: perAlliance(function (alliance) {
      return alliance.score;
    })});

    if (isQualification) {
      const rankingPoints = collectRows(alliances, "rankingPoints");
      rankingPoints.filter(function (row) {
        return row.numbers.some(function (number) {
          return number > 0;
        });
      }).forEach(function (row) {
        rows.push({kind: "rp", name: row.name, values: row.numbers.map(function (number) {
          return number > 0 ? GameView.formatNumber(number) : "";
        })});
      });
      if (rankingPoints.length > 0) {
        rows.push({kind: "rp-total", name: "Ranking Points", values: perAlliance(function (alliance) {
          return alliance.rpTotal;
        })});
      }
    }
    if (isPlayoff) {
      rows.push({kind: "wins", name: "Wins", values: perAlliance(function (alliance) {
        return alliance.wins;
      })});
    }
    return rows;
  };

  window.DisplayShared = {
    // ---------------------------------------------------------------------------------------------------------------
    // Pure helpers
    // ---------------------------------------------------------------------------------------------------------------

    fieldSlots: fieldSlots,

    escapeHtml: escapeHtml,

    getAvatarUrl: function (teamId) {
      return "/api/teams/" + teamId + "/avatar";
    },

    // Returns the name of a match as displays show it.
    matchName: function (match) {
      if (!match || !match.LongName) {
        return "";
      }
      return match.NameDetail ? match.LongName + " \u2013 " + match.NameDetail : match.LongName;
    },

    // Joins the label of what the robots are doing with the name of the phase, leaving the phase out when it only
    // repeats the label (a phase called "Autonomous", "Teleop" or "Pause").
    stateLabel: function (stateText, phaseName) {
      stateText = stateText || "";
      if (!phaseName || stateText.toUpperCase().indexOf(phaseName.toUpperCase()) === 0) {
        return stateText;
      }
      return stateText + " \u2013 " + phaseName;
    },

    // Converts a matchTime message into what a clock shows. Returns null until the game has arrived.
    //   state: a value of matchStates.
    //   stateText: what the robots are doing ("AUTONOMOUS", "TELEOPERATED", "PAUSE"), or the state outside a match.
    //   stateLabel: the same with the name of the phase added when it says more.
    //   countdownSec, countdown: the countdown as a number of seconds and as "m:ss".
    //   phaseName: the name of the phase that is running, or "" when none is.
    //   label: what goes beside the countdown: the phase that is running, or the name of the game when none is.
    //   robots: what the robots are told in the phase that is running or, as the match starts, in the first one
    //     ("auto", "teleop" or "disabled"); "" otherwise.
    clock: function (data) {
      let clock = null;
      translateMatchTime(data, function (matchState, matchStateText, countdownSec, phase) {
        const phaseName = phase === null ? "" : phase.name;
        const robotsPhase = matchState === "START_MATCH" ? GameView.phaseAt(0) : phase;
        clock = {
          state: matchState,
          stateText: matchStateText || "",
          stateLabel: DisplayShared.stateLabel(matchStateText, phaseName),
          countdownSec: countdownSec,
          countdown: getCountdownString(countdownSec),
          phaseName: phaseName,
          label: phase === null ? GameView.view.name : phaseName,
          robots: robotsPhase === null ? "" : robotsPhase.robots,
        };
      });
      return clock;
    },

    // Returns the name of the alliance that plays from a station of the field ("R1" to "B3"), or "" if there is none.
    stationAllianceName: function (stationId) {
      const fieldSlot = fieldSlots.find(function (candidate) {
        return candidate.stationPrefix === String(stationId)[0];
      });
      const alliance = fieldSlot ? GameView.alliance(GameView.allianceIdForSlot(fieldSlot.slot)) : null;
      return alliance === null ? "" : alliance.name;
    },

    // Returns an alliance's score from a realtimeScore message as text, or "" if the message does not have it.
    scoreText: function (score, allianceId) {
      const alliance = getAllianceScore(score, allianceId);
      return alliance === null ? "" : GameView.formatNumber(alliance.score);
    },

    // Returns the game's audience items for one alliance as [{ref, name, text, long}], with their values from a
    // realtimeScore message. The values are blank when no message is given.
    liveItems: function (score, allianceId) {
      if (!GameView.isReady()) {
        return [];
      }
      const items = [];
      GameView.view.audienceLive.forEach(function (ref) {
        const item = describeItem(ref);
        if (item === null) {
          return;
        }
        let text = "";
        if (score) {
          if (item.kind === "element") {
            text = getElementText(item, getRawValues(score, allianceId, item.element));
          } else {
            text = getValueText(getComputedValue(score, allianceId, item));
          }
        }
        items.push({ref: ref, name: item.name, text: text, long: text.length > liveValueShortLength});
      });
      return items;
    },

    // Returns the game's indicators for one alliance as [{id, name, lit}]. An indicator is lit when its live or
    // derived value is true or a number other than zero. None is lit when no message is given.
    indicators: function (score, allianceId) {
      if (!GameView.isReady()) {
        return [];
      }
      const indicators = [];
      GameView.view.indicators.forEach(function (id) {
        const item = describeItem(id);
        if (item === null || item.kind === "element") {
          return;
        }
        const value = score ? getComputedValue(score, allianceId, item) : undefined;
        indicators.push({
          id: id, name: item.name, lit: value === true || (typeof value === "number" && value !== 0),
        });
      });
      return indicators;
    },

    // Returns the markup of the items shown beside an alliance's score: one box per audience item, then the
    // indicators as badges in columns. The values are filled in afterwards, from each realtimeScore message.
    scoreItemsHtml: function () {
      let html = "";
      this.liveItems(null, "").forEach(function (item) {
        html += `<div class="score-live"><div class="score-live-value"></div>` +
          `<div class="score-live-name">${escapeHtml(item.name)}</div></div>`;
      });
      const indicators = this.indicators(null, "");
      for (let i = 0; i < indicators.length; i += indicatorsPerColumn) {
        html += `<div class="score-indicators">`;
        indicators.slice(i, i + indicatorsPerColumn).forEach(function (indicator) {
          html += `<div class="score-indicator" data-lit="false">${escapeHtml(indicator.name)}</div>`;
        });
        html += `</div>`;
      }
      return html;
    },

    // Returns the width in pixels of the markup that scoreItemsHtml produces.
    scoreItemsWidth: function () {
      const numIndicatorColumns = Math.ceil(this.indicators(null, "").length / indicatorsPerColumn);
      return this.liveItems(null, "").length * liveItemWidth + numIndicatorColumns * indicatorColumnWidth;
    },

    // Returns the id, in a scorePosted message, of the alliance in a layout slot ("red" or "blue").
    postedAllianceId: function (data, slot) {
      const alliances = data.alliances || {};
      const id = GameView.allianceIdForSlot(slot);
      if (alliances[id]) {
        return id;
      }
      // The match was played with a game whose alliances are not the active game's: take them in that game's order.
      const order = data.allianceOrder && data.allianceOrder.length > 0 ? data.allianceOrder : Object.keys(alliances);
      return order[slot === "blue" ? 1 : 0] || "";
    },

    // Describes the final score screen for a scorePosted message. slots lists the layout slot ("red" or "blue") shown
    // on each side, left first. Returns:
    //   matchName, isQualification, isPlayoff, tiebreak (text, or "" if no tiebreaker decided the match).
    //   sides: for each side {allianceId, score, result ("winner", "tie" or ""), resultLabel, won, playoffAlliance,
    //     wins, destination, teams: [{teamId, offField, card, cardColor, rank, rankChange ("up", "down" or "")}]}.
    //   rows: [{kind, name, values}] with one value per side; see getFinalRows.
    finalScore: function (data, slots) {
      const match = data.match || {};
      const isQualification = match.Type === matchTypeQualification;
      const isPlayoff = match.Type === matchTypePlayoff;
      const allianceIds = slots.map(function (slot) {
        return DisplayShared.postedAllianceId(data, slot);
      });
      const alliances = allianceIds.map(function (allianceId) {
        return (data.alliances || {})[allianceId] || null;
      });
      return {
        matchName: DisplayShared.matchName(match),
        isQualification: isQualification,
        isPlayoff: isPlayoff,
        tiebreak: data.tiebreak ? "Tiebreaker: " + data.tiebreak : "",
        sides: slots.map(function (slot, index) {
          const playoffAlliance = match[`Playoff${getFieldSlot(slot).matchPrefix}Alliance`] || 0;
          return getFinalSide(allianceIds[index], alliances[index], playoffAlliance, data.cards);
        }),
        rows: getFinalRows(alliances, isQualification, isPlayoff),
      };
    },

    // Returns the markup of the rows of the final score: the first side's value, the name, the second side's value.
    finalRowsHtml: function (rows) {
      return rows.map(function (row) {
        return `<div class="final-row" data-kind="${escapeHtml(row.kind)}">` +
          `<div class="final-value">${escapeHtml(row.values[0])}</div>` +
          `<div class="final-label">${escapeHtml(row.name)}</div>` +
          `<div class="final-value">${escapeHtml(row.values[1])}</div></div>`;
      }).join("");
    },

    // Returns the line height and font size, in pixels, at which the given number of final score rows fit.
    finalRowMetrics: function (numRows) {
      const lineHeight = Math.max(
        finalRowMinHeight, Math.min(finalRowMaxHeight, Math.floor(finalRowsHeight / Math.max(1, numRows)))
      );
      return {lineHeight: lineHeight, fontSize: Math.round(lineHeight * 2 / 3)};
    },

    // Returns the markup of one side's teams on the final score screen. The rank is shown only when showRank is set.
    finalTeamsHtml: function (teams, showRank) {
      return teams.map(function (team) {
        let html = `<div class="final-team-row">` +
          `<img class="final-team-avatar" src="${escapeHtml(DisplayShared.getAvatarUrl(team.teamId))}"/>` +
          `<div class="final-team-number">${escapeHtml(team.teamId)}</div>` +
          `<div class="final-team-card">`;
        if (team.card !== "") {
          html += `<div data-card="${escapeHtml(team.card)}" ` +
            `style="background-color: ${escapeHtml(team.cardColor)};"></div>`;
        }
        html += `</div>`;
        if (showRank) {
          html += `<div class="final-team-rank">`;
          if (team.rankChange !== "") {
            html += `<img src="/static/img/rank-${team.rankChange}.svg"/>`;
          }
          html += `<div>${escapeHtml(team.rank)}</div></div>`;
        }
        return html + `</div>`;
      }).join("");
    },

    // Returns the columns of the rankings table that come from the game's ranking order, as [{name, decimals, index}],
    // leaving out the coin flip. The index is the column's place in the list the server sent, which is also the place
    // of its value in each team's Sort list.
    rankingColumns: function (columns) {
      const visible = [];
      (columns || []).forEach(function (column, index) {
        if (!column.Random) {
          visible.push({name: column.Name, decimals: column.Decimals || 0, index: index});
        }
      });
      return visible;
    },

    // Returns a team's value for one of the columns from rankingColumns, to the number of decimals the column asks for.
    rankingValue: function (ranking, column) {
      const value = (ranking.Sort || [])[column.index];
      return typeof value === "number" ? value.toFixed(column.decimals) : "";
    },

    // Returns the width, as a percentage of the table, of each column of the rankings table other than the team name,
    // which has a fifth of the table to itself.
    rankingsFieldWidth: function (columns) {
      const numFixedFields = 6;  // Rank, team, ranking points, record, disqualifications, played.
      return Math.round(8000 / (numFixedFields + this.rankingColumns(columns).length)) / 100;
    },

    // Returns the header row of the rankings table for the columns the server sent.
    rankingsHeaderHtml: function (columns) {
      let html = `<tr><td class="team-field">Rank</td><td class="team-field">Team</td>` +
        `<td class="team-nickname">Name</td>`;
      this.rankingColumns(columns).forEach(function (column) {
        html += `<td class="team-field">${escapeHtml(column.name)}</td>`;
      });
      return html + `<td class="team-field">RP</td><td class="team-field">W-L-T</td><td class="team-field">DQ</td>` +
        `<td class="team-field">Played</td></tr>`;
    },

    // Returns the body of the rankings table for the response of /api/rankings.
    rankingsRowsHtml: function (data) {
      const columns = this.rankingColumns(data.Columns);
      let html = `<tbody>`;
      (data.Rankings || []).forEach(function (ranking) {
        html += `<tr><td class="team-field">${escapeHtml(ranking.Rank)}</td>` +
          `<td class="team-field">${escapeHtml(ranking.TeamId)}</td>` +
          `<td class="team-nickname">${escapeHtml(ranking.Nickname)}</td>`;
        columns.forEach(function (column) {
          html += `<td class="team-field">${escapeHtml(DisplayShared.rankingValue(ranking, column))}</td>`;
        });
        html += `<td class="team-field">${escapeHtml(GameView.formatNumber(ranking.RP))}</td>` +
          `<td class="team-field">${escapeHtml(ranking.Wins + "-" + ranking.Losses + "-" + ranking.Ties)}</td>` +
          `<td class="team-field">${escapeHtml(ranking.Disqualifications)}</td>` +
          `<td class="team-field">${escapeHtml(ranking.Played)}</td></tr>`;
      });
      return html + `</tbody>`;
    },

    // ---------------------------------------------------------------------------------------------------------------
    // Overlay
    // ---------------------------------------------------------------------------------------------------------------

    // Works out which side of the screen each layout slot is on, and marks the elements that take an alliance's
    // colour accordingly.
    applyDisplaySides: function (urlParams) {
      const reversed = urlParams.get("reversed");
      const redSide = reversed === "true" ? "right" : "left";
      const blueSide = reversed === "true" ? "left" : "right";
      $(".reversible-left").attr("data-reversed", reversed);
      $(".reversible-right").attr("data-reversed", reversed);
      return {redSide: redSide, blueSide: blueSide};
    },

    // Rebuilds the items beside each score for the active game.
    buildScoreItems: function () {
      $(".score-items").html(this.scoreItemsHtml());
    },

    // Handles a websocket message to update the teams for the current match. Returns the match.
    handleMatchLoad: function (data, redSide, blueSide) {
      const currentMatch = data.Match;
      const isPlayoff = currentMatch.Type === matchTypePlayoff;
      const allianceSize = GameView.isReady() ? GameView.view.allianceSize : overlayTeamRows;
      const sides = {red: redSide, blue: blueSide};
      fieldSlots.forEach(function (fieldSlot) {
        const side = sides[fieldSlot.slot];
        for (let position = 1; position <= overlayTeamRows; position++) {
          const teamId = currentMatch[fieldSlot.matchPrefix + position];
          const team = data.Teams[fieldSlot.stationPrefix + position];
          const yellowCard = isPlayoff ? data[`Playoff${fieldSlot.matchPrefix}AllianceYellowCard`] :
            (team ? team.YellowCard : false);
          // A game with smaller alliances leaves the last places on each side empty.
          const unused = position > allianceSize;
          $(`#${side}Team${position}`).text(teamId).attr("data-yellow-card", yellowCard === true)
            .attr("data-unused", unused);
          $(`#${side}Team${position}Avatar`).attr("src", DisplayShared.getAvatarUrl(teamId))
            .attr("data-unused", unused);
        }

        if (isPlayoff) {
          $(`#${side}PlayoffAlliance`).text(currentMatch[`Playoff${fieldSlot.matchPrefix}Alliance`]);
          if (data.Matchup && data.Matchup.NumWinsToAdvance > 1) {
            $(`#${side}PlayoffAllianceWins`).text(data.Matchup[`${fieldSlot.matchPrefix}AllianceWins`]);
          }
        } else {
          $(`#${side}PlayoffAlliance`).text("");
        }
      });

      $(".playoff-alliance").toggle(isPlayoff);
      if (isPlayoff && data.Matchup && data.Matchup.NumWinsToAdvance > 1) {
        $("#playoffSeriesStatus").css("display", "flex");
      } else {
        $("#playoffSeriesStatus").hide();
      }

      $("#matchName").text(this.matchName(currentMatch));
      const timeoutNextMatchName = data.BreakNextMatchName || "";
      const timeoutDetailOpacity = $("#timeoutBreakDescription").css("opacity");
      $("#timeoutNextMatch").toggle(timeoutNextMatchName !== "").css(
        "opacity", timeoutNextMatchName === "" ? 0 : timeoutDetailOpacity
      );
      $("#timeoutNextMatchName").text(timeoutNextMatchName);
      $("#timeoutBreakDescription").text(data.BreakDescription);
      return currentMatch;
    },

    // Handles a websocket message to update the countdown and the name of the phase beside it.
    handleMatchTime: function (data) {
      const clock = this.clock(data);
      if (clock === null) {
        return;
      }
      $("#matchTime").text(clock.countdown);
      $("#matchPhase").text(clock.label);
    },

    // Handles a websocket message to update each alliance's score, audience items and indicators.
    handleRealtimeScore: function (data, redSide, blueSide) {
      const sides = {red: redSide, blue: blueSide};
      fieldSlots.forEach(function (fieldSlot) {
        const side = sides[fieldSlot.slot];
        const allianceId = GameView.allianceIdForSlot(fieldSlot.slot);
        $(`#${side}ScoreNumber`).text(DisplayShared.scoreText(data, allianceId));

        // The markup was built from the same lists, so the values go in by position.
        const items = $(`#${side}ScoreItems`);
        const values = items.find(".score-live-value");
        DisplayShared.liveItems(data, allianceId).forEach(function (item, index) {
          values.eq(index).text(item.text).attr("data-long", item.long);
        });
        const badges = items.find(".score-indicator");
        DisplayShared.indicators(data, allianceId).forEach(function (indicator, index) {
          badges.eq(index).attr("data-lit", indicator.lit);
        });
      });
    },
  };
})(window);
