// Holds the active game's description (the "view" sent in the "game" websocket message) and the helpers every page
// shares for reading it. Nothing in the browser code may hard-code the name of an alliance, phase, scoring element,
// foul or ranking point: it all comes from here.
//
// Load this file before match_timing.js and before any page script, and route the "game" websocket message to
// handleGame.

const GameView = {
  view: null,
  listeners: [],

  // Stores a new view, updates the alliance colour CSS variables, and tells every listener.
  set: function (view) {
    this.view = view;
    this.applyColors();
    this.listeners.forEach(function (listener) {
      listener(view);
    });
  },

  // Registers a function to call with the view now (if one has arrived) and each time the game changes.
  onChange: function (listener) {
    this.listeners.push(listener);
    if (this.view !== null) {
      listener(this.view);
    }
  },

  isReady: function () {
    return this.view !== null;
  },

  // Sets --alliance-<index>-color and --alliance-<id>-color on the page so stylesheets never name a colour.
  applyColors: function () {
    const style = document.documentElement.style;
    this.view.alliances.forEach(function (alliance, index) {
      style.setProperty("--alliance-" + index + "-color", alliance.color);
      style.setProperty("--alliance-" + alliance.id + "-color", alliance.color);
    });
  },

  alliances: function () {
    return this.view === null ? [] : this.view.alliances;
  },

  alliance: function (id) {
    return this.alliances().find(function (alliance) {
      return alliance.id === id;
    }) || null;
  },

  allianceAt: function (index) {
    return this.alliances()[index] || null;
  },

  allianceIndex: function (id) {
    return this.alliances().findIndex(function (alliance) {
      return alliance.id === id;
    });
  },

  // Returns the layout slot of an alliance: "red" for the first and "blue" for the second. These are positions on
  // the screen and in the match record, kept from the two-alliance layouts; the colour comes from the game.
  slot: function (id) {
    return ["red", "blue"][this.allianceIndex(id)] || "";
  },

  // Returns the alliance id occupying a layout slot ("red" or "blue").
  allianceIdForSlot: function (slot) {
    const alliance = this.allianceAt(slot === "blue" ? 1 : 0);
    return alliance === null ? "" : alliance.id;
  },

  phases: function () {
    return this.view === null ? [] : this.view.phases;
  },

  phase: function (id) {
    return this.phases().find(function (phase) {
      return phase.id === id;
    }) || null;
  },

  phaseAt: function (index) {
    return this.phases()[index] || null;
  },

  element: function (id) {
    if (this.view === null) {
      return null;
    }
    return this.view.elements.find(function (element) {
      return element.id === id;
    }) || null;
  },

  // Returns the elements of one scope ("alliance", "robot" or "field"), in display order.
  elementsFor: function (scope) {
    if (this.view === null) {
      return [];
    }
    return this.view.elements.filter(function (element) {
      return element.scope === scope;
    }).sort(function (a, b) {
      return a.order - b.order;
    });
  },

  foulType: function (id) {
    if (this.view === null) {
      return null;
    }
    return this.view.foulTypes.find(function (foulType) {
      return foulType.id === id;
    }) || null;
  },

  card: function (id) {
    if (this.view === null) {
      return null;
    }
    return this.view.cards.find(function (card) {
      return card.id === id;
    }) || null;
  },

  // Returns the total of a counter's raw value, which is sent as {counts: {phase: n}}.
  counterTotal: function (raw) {
    if (!raw || !raw.counts) {
      return 0;
    }
    return Object.values(raw.counts).reduce(function (sum, count) {
      return sum + count;
    }, 0);
  },

  // Converts an element's raw value, as it appears under "state" in the realtimeScore message, into the plain
  // value a person would read: a count, true or false, an option id, seconds, or a number.
  plainValue: function (element, raw) {
    raw = raw || {};
    switch (element.type) {
      case "counter":
        return this.counterTotal(raw);
      case "boolean":
        return raw.bool === true;
      case "enum":
        return raw.option || (element.options.length > 0 ? element.options[0].id : "");
      case "timer":
        return raw.seconds || 0;
      default:
        return raw.number || 0;
    }
  },

  // Returns the display name of an enum option.
  optionName: function (element, optionId) {
    const option = (element.options || []).find(function (candidate) {
      return candidate.id === optionId;
    });
    return option ? option.name : optionId;
  },

  // Formats a computed number for display: whole numbers without decimals, others to at most two places.
  formatNumber: function (value) {
    if (typeof value !== "number") {
      return value === null || value === undefined ? "" : String(value);
    }
    return String(Math.round(value * 100) / 100);
  },
};

// Handles the "game" websocket message.
const handleGame = function (data) {
  GameView.set(data);
};
