// The Devices page, and the code the Bindings and virtual devices pages share with it (DevicePages): the live feed
// of every device, sending requests, and drawing values and outputs. Pages that only need the shared part load this
// file too; the Devices page itself starts only where its container exists.

// ---------------------------------------------------------------------------------------------------------------
// Shared by the device pages

const DevicePages = {
  state: null,          // The last "devices" message: devices, transports, faults, drivers, virtual, mqtt.
  clockOffset: 0,       // The server's clock minus this browser's, in milliseconds.
  lastSounds: {},       // The last sound played on each sound output, by "device/channel".
  listeners: [],        // Called with the state whenever it changes.
  eventListeners: [],   // Called with each device event after it is applied.

  // Opens the feed. extraEvents are more websocket message handlers (the "game" message, for one).
  connect: function (extraEvents) {
    const events = Object.assign({
      devices: function (event) {
        DevicePages.setState(event.data);
      },
      deviceEvent: function (event) {
        DevicePages.applyEvent(event.data);
      },
    }, extraEvents || {});
    new CheesyWebsocket("/setup/devices/websocket", events, {
      open: function () {
        $("#feedStatus").addClass("d-none");
      },
      close: function () {
        $("#feedStatus").removeClass("d-none");
      },
    });
    DevicePages.animate();
  },

  onChange: function (listener) {
    this.listeners.push(listener);
    if (this.state !== null) {
      listener(this.state);
    }
  },

  onEvent: function (listener) {
    this.eventListeners.push(listener);
  },

  setState: function (state) {
    this.state = state;
    this.clockOffset = state.time - Date.now();
    this.listeners.forEach(function (listener) {
      listener(state);
    });
  },

  // Applies an input or output event to the state, so that values show at once rather than with the next state.
  applyEvent: function (event) {
    const data = event.data || {};
    const info = this.device(data.id);
    if (info !== null) {
      if (event.type === "device.input") {
        info.inputs = info.inputs || {};
        info.inputs[data.channel] = {value: data.value, conf: data.conf, time: event.time};
      } else if (event.type === "device.output") {
        info.outputs = info.outputs || {};
        if (data.command === null) {
          delete info.outputs[data.channel];
        } else if (data.command.play) {
          this.lastSounds[data.id + "/" + data.channel] = {play: data.command.play, time: event.time};
        } else {
          info.outputs[data.channel] = data.command;
        }
      }
    }
    this.eventListeners.forEach(function (listener) {
      listener(event);
    });
  },

  devices: function () {
    return this.state === null ? [] : this.state.devices;
  },

  device: function (id) {
    return this.devices().find(function (info) { return info.id === id; }) || null;
  },

  channel: function (id, channelId) {
    const info = this.device(id);
    if (info === null || !info.manifest) {
      return null;
    }
    return (info.manifest.channels || []).find(function (channel) { return channel.id === channelId; }) || null;
  },

  // What drives an output channel: {owner, binding, active}, or null.
  driverOf: function (id, channelId) {
    if (this.state === null) {
      return null;
    }
    return this.state.drivers.find(function (driver) {
      return driver.device === id && driver.channel === channelId;
    }) || null;
  },

  // The field clock, on which strips blink in step with the real ones.
  fieldTime: function () {
    return Date.now() + this.clockOffset;
  },

  // Sends a request and returns a promise of its JSON answer. A refusal rejects with the server's message.
  request: function (method, url, body) {
    const options = {method: method, headers: {}};
    if (body !== undefined) {
      options.body = typeof body === "string" ? body : JSON.stringify(body);
      options.headers["Content-Type"] = typeof body === "string" ? "text/plain" : "application/json";
    }
    return fetch(url, options).then(function (response) {
      return response.text().then(function (text) {
        let data = {};
        try {
          data = text ? JSON.parse(text) : {};
        } catch (e) {
          data = {error: text};
        }
        if (!response.ok) {
          const error = new Error(data.error || ("The FMS answered " + response.status));
          error.data = data;
          throw error;
        }
        return data;
      });
    });
  },

  post: function (url, body) {
    return this.request("POST", url, body === undefined ? {} : body);
  },

  // Shows a short message at the top of the page; kind is a Bootstrap colour (success, danger, ...).
  notify: function (message, kind) {
    const box = $("<div class='alert alert-dismissible fade show py-2 mb-2'>").addClass("alert-" + (kind || "info"))
      .text(message).append("<button type='button' class='btn-close py-2' data-bs-dismiss='alert'></button>");
    $("#messages").append(box);
    setTimeout(function () { box.alert("close"); }, kind === "danger" ? 12000 : 5000);
  },

  fail: function (error) {
    DevicePages.notify(error.message, "danger");
  },

  escape: function (text) {
    return $("<div>").text(text === null || text === undefined ? "" : String(text)).html();
  },

  isZeroTime: function (time) {
    return !time || time.startsWith("0001-");
  },

  // How long ago a time was, in words.
  ago: function (time) {
    if (this.isZeroTime(time)) {
      return "never";
    }
    const seconds = Math.max(0, (this.fieldTime() - Date.parse(time)) / 1000);
    if (seconds < 2) {
      return "just now";
    }
    if (seconds < 90) {
      return Math.round(seconds) + " s ago";
    }
    if (seconds < 5400) {
      return Math.round(seconds / 60) + " min ago";
    }
    if (seconds < 36 * 3600) {
      return Math.round(seconds / 3600) + " h ago";
    }
    return new Date(time).toLocaleString();
  },

  duration: function (seconds) {
    seconds = Math.floor(seconds);
    const h = Math.floor(seconds / 3600);
    const m = Math.floor((seconds % 3600) / 60);
    const s = seconds % 60;
    if (h > 0) {
      return h + " h " + String(m).padStart(2, "0") + " min";
    }
    if (m > 0) {
      return m + " min " + String(s).padStart(2, "0") + " s";
    }
    return s + " s";
  },

  // The HTML of an input's value: a lit badge for a digital input, the number or text otherwise.
  formatInput: function (channel, input) {
    if (!input) {
      return "<span class='text-secondary'>no reading yet</span>";
    }
    const value = input.value;
    let html;
    if (channel && channel.type === "digital") {
      const on = value === 1 || value === true;
      const label = channel.safety ? (on ? "1 · safe" : "0 · STOP") : (on ? "1" : "0");
      const badge = channel.safety ? (on ? "text-bg-success" : "text-bg-danger") :
        (on ? "text-bg-warning" : "text-bg-secondary");
      html = "<span class='badge " + badge + "'>" + label + "</span>";
    } else if (typeof value === "number") {
      html = String(Math.round(value * 1000) / 1000);
      if (channel && channel.unit) {
        html += " " + this.escape(channel.unit);
      }
    } else {
      html = "<code>" + this.escape(value) + "</code>";
    }
    if (input.conf !== undefined && input.conf !== null) {
      html += " <span class='text-secondary small'>conf " + input.conf + "</span>";
    }
    return html;
  },

  // The HTML of what an output shows. LED strips are canvases the animation loop draws; source says where their
  // command is read from ("registry" for the FMS's view, "virtual" for a virtual device's own).
  outputHtml: function (id, channel, source) {
    const key = this.escape(id) + "' data-channel='" + this.escape(channel.id) + "' data-source='" +
      (source || "registry");
    switch (channel.type) {
      case "led_strip": {
        const width = Math.min(360, Math.max(60, channel.pixels * 8));
        return "<canvas class='led-strip' width='" + width + "' height='12' data-pixels='" + channel.pixels +
          "' data-device='" + key + "'></canvas>";
      }
      case "digital":
      case "relay":
        return "<span class='lamp' data-device='" + key + "'></span>";
      case "pwm":
        return "<span class='pwm-bar' data-device='" + key + "'><div style='width: 0%'></div></span> " +
          "<span class='pwm-text small' data-device='" + key + "'></span>";
      case "display":
        return "<span class='display-box' data-device='" + key + "'>&nbsp;</span>";
      case "sound":
        return "<span class='sound-text small' data-device='" + key + "'>silent</span>";
    }
    return "";
  },

  // The command an output element shows, from the FMS's registry or from the virtual device itself.
  commandFor: function (element) {
    const id = element.getAttribute("data-device");
    const channel = element.getAttribute("data-channel");
    if (element.getAttribute("data-source") === "virtual") {
      const view = (this.state ? this.state.virtual : []).find(function (v) { return v.id === id; });
      return view && view.outputs ? view.outputs[channel] || null : null;
    }
    const info = this.device(id);
    return info && info.outputs ? info.outputs[channel] || null : null;
  },

  // Draws every output on the page, about twenty times a second.
  animate: function () {
    const draw = function () {
      const t = DevicePages.fieldTime();
      document.querySelectorAll("canvas.led-strip[data-device]").forEach(function (canvas) {
        const pixels = parseInt(canvas.getAttribute("data-pixels")) || 1;
        const colors = LedPatterns.render(DevicePages.commandFor(canvas), pixels, t);
        const context = canvas.getContext("2d");
        const width = canvas.width / pixels;
        context.fillStyle = "#000";
        context.fillRect(0, 0, canvas.width, canvas.height);
        colors.forEach(function (color, i) {
          context.fillStyle = "rgb(" + color[0] + "," + color[1] + "," + color[2] + ")";
          context.fillRect(Math.floor(i * width) + 1, 1, Math.max(1, Math.ceil(width) - 2), canvas.height - 2);
        });
      });
      document.querySelectorAll(".lamp[data-device]").forEach(function (lamp) {
        const command = DevicePages.commandFor(lamp);
        const on = command !== null && LedPatterns.renderSingle(command, t)[0] > 0;
        lamp.style.background = on ? "#ffd54f" : "#222";
        lamp.style.boxShadow = on ? "0 0 10px #ffd54f" : "none";
      });
      document.querySelectorAll(".pwm-bar[data-device]").forEach(function (bar) {
        const command = DevicePages.commandFor(bar);
        const level = command && typeof command.v === "number" ? command.v : 0;
        bar.firstChild.style.width = Math.round(level * 100) + "%";
      });
      document.querySelectorAll(".pwm-text[data-device]").forEach(function (text) {
        const command = DevicePages.commandFor(text);
        text.textContent = command && typeof command.v === "number" ? Math.round(command.v * 100) + "%" : "off";
      });
      document.querySelectorAll(".display-box[data-device]").forEach(function (box) {
        const command = DevicePages.commandFor(box);
        let text = "";
        if (command) {
          text = command.text !== undefined ? command.text : (command.v !== undefined ? String(command.v) : "");
        }
        box.textContent = text === "" ? " " : text;
      });
      document.querySelectorAll(".sound-text[data-device]").forEach(function (span) {
        const key = span.getAttribute("data-device") + "/" + span.getAttribute("data-channel");
        const sound = DevicePages.lastSounds[key];
        span.textContent = sound ? "played " + sound.play + " " + DevicePages.ago(sound.time) : "silent";
      });
    };
    setInterval(draw, 50);
  },

  // A short description of a device's channels, for a device waiting for approval.
  channelSummary: function (manifest) {
    if (!manifest) {
      return "has not sent its manifest yet";
    }
    const channels = manifest.channels || [];
    if (channels.length === 0) {
      return "no channels";
    }
    return channels.map(function (channel) {
      return channel.id + " (" + (channel.dir === "in" ? "" : "out ") + channel.type +
        (channel.safety ? ", safety" : "") + ")";
    }).join(", ");
  },

  // Which transports a device is connected over, with the remote address of each.
  presenceText: function (info) {
    return Object.keys(info.presence || {}).sort().map(function (transport) {
      const remote = info.presence[transport].remote;
      return transport + (remote && remote !== transport ? " " + remote : "");
    }).join(", ");
  },
};

// ---------------------------------------------------------------------------------------------------------------
// The Devices page

const DevicesPage = {
  signatures: {},   // The last drawn signature of each device card, to redraw a card only when it changed.
  pendingSignature: "", // The same for the list of devices waiting for approval.
  token: null,      // The token on show, with its device: {token, id, name}.

  start: function () {
    DevicePages.onChange(DevicesPage.render);
    DevicePages.onEvent(DevicesPage.flash);
    DevicePages.connect();

    $("#addDevice").on("click", function () {
      $("#newDeviceId, #newDeviceName").val("").removeData("edited");
      $("#addDeviceError").addClass("d-none");
      $("#addDeviceModal").modal("show");
      setTimeout(function () { $("#newDeviceName").trigger("focus"); }, 300);
    });
    $("#newDeviceName").on("input", function () {
      if (!$("#newDeviceId").data("edited")) {
        $("#newDeviceId").val($(this).val().toLowerCase().replace(/[^a-z0-9]+/g, "-").replace(/^-+|-+$/g, "")
          .slice(0, 40));
      }
    });
    $("#newDeviceId").on("input", function () {
      $(this).data("edited", true);
    });
    $("#addDeviceForm").on("submit", function (event) {
      event.preventDefault();
      DevicePages.post("/setup/devices/create", {id: $("#newDeviceId").val(), name: $("#newDeviceName").val()})
        .then(function (data) {
          $("#addDeviceModal").modal("hide");
          DevicesPage.showToken(data);
        })
        .catch(function (error) {
          $("#addDeviceError").removeClass("d-none").text(error.message);
        });
    });
    $("#tokenHost, #tokenPort").on("input change", DevicesPage.renderSnippets);
    $(document).on("click", "[data-copy]", function () {
      const text = $($(this).attr("data-copy")).text();
      navigator.clipboard.writeText(text).then(function () {
        DevicePages.notify("Copied.", "success");
      }, function () {
        DevicePages.notify("The browser would not copy; select the text and copy it by hand.", "warning");
      });
    });
    $("#tokenModal").on("hidden.bs.modal", function () {
      // The token is not kept anywhere once its window is closed.
      DevicesPage.token = null;
      $("#tokenValue").text("");
      $(".snippet").text("");
    });
    $(document).on("click", "[data-action]", DevicesPage.action);
    $(document).on("change", "[data-setting]", DevicesPage.setting);
    $(document).on("click", "[data-test]", DevicesPage.testOutput);
  },

  render: function (state) {
    DevicesPage.renderFaults(state);
    DevicesPage.renderPending(state);
    DevicesPage.renderDevices(state);
    DevicesPage.renderTransports(state);
    DevicesPage.updateLive(state);
  },

  renderFaults: function (state) {
    const container = $("#faults").empty();
    if (state.faults.length === 0) {
      return;
    }
    const box = $("<div class='alert alert-danger'>");
    box.append("<h5 class='alert-heading'>Device faults</h5><p class='mb-2'>A fault holds its stop until it is " +
      "cleared. Clear it once the device is back and safe.</p>");
    const list = $("<ul class='mb-2'>");
    state.faults.forEach(function (fault) {
      const info = DevicePages.device(fault.device);
      list.append($("<li>").text((info ? info.name : fault.device) + " · binding " + fault.binding + ": " +
        fault.reason + " (" + DevicePages.ago(fault.time) + ")"));
    });
    box.append(list);
    box.append("<button class='btn btn-light btn-sm' data-action='clear_all_faults'>Clear all faults</button>");
    container.append(box);
  },

  renderPending: function (state) {
    const pending = state.devices.filter(function (info) { return info.state === "pending"; });
    const signature = JSON.stringify(pending.map(function (info) {
      return [info.id, info.name, info.online, info.presence, info.manifest];
    }));
    if (signature === DevicesPage.pendingSignature) {
      return;
    }
    DevicesPage.pendingSignature = signature;
    const container = $("#pending").empty();
    if (pending.length === 0) {
      return;
    }
    const card = $("<div class='card device-pending mb-3'>");
    card.append("<div class='card-header bg-warning-subtle'><strong>Waiting for approval (" + pending.length +
      ")</strong> <span class='small'>A device that is not approved can announce itself, nothing more. Approve the " +
      "ones you recognise.</span></div>");
    const body = $("<div class='list-group list-group-flush'>");
    pending.forEach(function (info) {
      const e = DevicePages.escape;
      const online = info.online ? "<span class='badge text-bg-success'>online</span> " +
        e(DevicePages.presenceText(info)) : "<span class='badge text-bg-secondary'>offline</span>";
      body.append(
        "<div class='list-group-item d-flex align-items-center gap-3 flex-wrap'>" +
        "<div class='me-auto'><div class='device-name fw-bold'>" + e(info.name) + " <code>" + e(info.id) +
        "</code></div>" +
        "<div class='small'>" + online + " · first seen at " + e(new Date(info.createdAt).toLocaleTimeString()) +
        "</div>" +
        "<div class='small text-secondary'>Channels: " + e(DevicePages.channelSummary(info.manifest)) + "</div></div>" +
        "<button class='btn btn-success btn-lg btn-approve' data-action='approve' data-id='" + e(info.id) +
        "'><i class='bi bi-check-lg'></i> Approve</button>" +
        "<button class='btn btn-outline-danger' data-action='block' data-id='" + e(info.id) + "'>Block</button></div>");
    });
    container.append(card.append(body));
  },

  renderDevices: function (state) {
    const order = {approved: 0, blocked: 1};
    const devices = state.devices.filter(function (info) { return info.state !== "pending"; }).sort(function (a, b) {
      const rank = function (info) { return info.builtIn ? 2 : order[info.state] || 0; };
      return rank(a) - rank(b) || a.id.localeCompare(b.id);
    });
    const container = $("#deviceList");
    if (devices.length === 0) {
      const waiting = state.devices.length > 0;
      container.html("<div class='card bg-body-tertiary text-secondary'><div class='card-body'>" + (waiting ?
        "No approved devices yet: approve the ones above that you recognise." : "No devices yet. Flash a board " +
        "with an example sketch and it appears here, waiting for approval; or click <b>Add device</b> to get a " +
        "token first; or try one on the <a href='/setup/devices/virtual'>virtual devices</a> page.") +
        "</div></div>");
      DevicesPage.signatures = {};
      return;
    }
    container.children(":not(.device-card)").remove();
    const ids = devices.map(function (info) { return info.id; });
    container.children(".device-card").each(function () {
      if (!ids.includes($(this).attr("data-card"))) {
        delete DevicesPage.signatures[$(this).attr("data-card")];
        $(this).remove();
      }
    });
    let previous = null;
    devices.forEach(function (info) {
      const drivers = state.drivers.filter(function (driver) { return driver.device === info.id; });
      const signature = JSON.stringify([info.name, info.state, info.notes, info.required, info.manifest, info.faults,
        info.builtIn, info.hasToken, info.online, Object.keys(info.presence || {}), drivers, info.stopBound]);
      let card = container.children(".device-card").filter(function () {
        return $(this).attr("data-card") === info.id;
      });
      // Text being typed (notes, a test's text) is not thrown away; a button that keeps the focus does not count.
      const editing = card.length > 0 &&
        card.find("textarea:focus, select:focus, input[type=text]:focus, input[type=number]:focus").length > 0;
      if (card.length === 0 || (DevicesPage.signatures[info.id] !== signature && !editing)) {
        const fresh = $(DevicesPage.cardHtml(info, drivers));
        if (card.length > 0) {
          // Keep what was typed into the test controls.
          card.find("[data-keep]").each(function () {
            fresh.find("[data-keep='" + $(this).attr("data-keep") + "']").val($(this).val());
          });
          card.replaceWith(fresh);
        }
        card = fresh;
        DevicesPage.signatures[info.id] = signature;
      }
      // Cards move only when the order changed: moving one would take the focus from what is typed in it.
      if (previous === null) {
        if (container.children(".device-card").first()[0] !== card[0]) {
          container.prepend(card);
        }
      } else if (card.prev()[0] !== previous[0]) {
        card.insertAfter(previous);
      }
      previous = card;
    });
  },

  cardHtml: function (info, drivers) {
    const e = DevicePages.escape;
    const id = e(info.id);
    let stateBadge = "<span class='badge text-bg-success'>approved</span>";
    if (info.builtIn) {
      stateBadge = "<span class='badge text-bg-info'>built in</span>";
    } else if (info.state === "blocked") {
      stateBadge = "<span class='badge text-bg-danger'>blocked</span>";
    }
    const online = info.online ? "<span class='badge text-bg-success'>online</span> <span class='small'>" +
      e(DevicePages.presenceText(info)) + "</span>" : "<span class='badge text-bg-secondary'>offline</span>";
    const manifest = info.manifest || {};
    let html = "<div class='card device-card mb-3" + (info.state === "blocked" ? " device-blocked" : "") +
      "' data-card='" + id + "'><div class='card-header'>" +
      "<span class='device-name'>" + e(info.name) + "</span> <code>" + id + "</code> " + stateBadge + " " + online;
    if (info.stopBound) {
      html += " <span class='badge text-bg-warning' title='A stop is bound to this device'>stop</span>";
    }
    html += "<span class='ms-auto'></span>";
    if (!info.builtIn) {
      html += "<div class='form-check form-switch mb-0 me-2' title='A match cannot start while a required device is " +
        "offline'><input class='form-check-input' type='checkbox' id='required-" + id + "' data-setting='required' " +
        "data-id='" + id + "'" + (info.required ? " checked" : "") + "><label class='form-check-label small' " +
        "for='required-" + id + "'>Required</label></div>";
      if (info.state === "approved") {
        html += "<button class='btn btn-sm btn-outline-info' data-action='identify' data-id='" + id + "'" +
          (info.online ? "" : " disabled") + " title='Flash the device so you can find it'>Identify</button>";
      }
      html += "<div class='dropdown'><button class='btn btn-sm btn-outline-secondary dropdown-toggle' " +
        "data-bs-toggle='dropdown'>More</button><ul class='dropdown-menu dropdown-menu-end'>" +
        "<li><a class='dropdown-item' href='#' data-action='rename' data-id='" + id + "'>Rename</a></li>";
      if (info.state === "approved") {
        html += "<li><a class='dropdown-item' href='#' data-action='reboot' data-id='" + id + "'>Reboot</a></li>" +
          "<li><a class='dropdown-item' href='#' data-action='token' data-id='" + id + "'>New token</a></li>" +
          "<li><a class='dropdown-item text-danger' href='#' data-action='block' data-id='" + id + "'>Block</a></li>";
      } else {
        html += "<li><a class='dropdown-item' href='#' data-action='unblock' data-id='" + id +
          "'>Unblock (make it pending)</a></li>";
      }
      html += "<li><hr class='dropdown-divider'></li><li><a class='dropdown-item text-danger' href='#' " +
        "data-action='delete' data-id='" + id + "'>Delete</a></li></ul></div>";
    }
    html += "</div><div class='card-body'>";
    html += "<div class='device-meta mb-2' data-meta='" + id + "'></div>";
    if (manifest.fw || manifest.sdk || manifest.board) {
      html += "<div class='device-meta mb-2'>" +
        (manifest.fw ? "<span>Firmware " + e(manifest.fw) + "</span>" : "") +
        (manifest.sdk ? "<span>SDK " + e(manifest.sdk) + "</span>" : "") +
        (manifest.board ? "<span>Board " + e(manifest.board) + "</span>" : "") + "</div>";
    }
    // A device inside the FMS or on its USB port needs no token; one on the network does.
    const local = manifest.sdk === "galaxia-virtual" || Object.keys(info.presence || {}).some(function (transport) {
      return transport === "virtual" || transport === "serial";
    });
    if (info.state === "approved" && !info.hasToken && !info.builtIn && !local) {
      html += "<div class='small text-warning mb-2'>This device has no token, so it can only connect over USB or as " +
        "a virtual device. Give it one (More &gt; New token) to connect over the network.</div>";
    }
    info.faults.forEach(function (fault) {
      html += "<div class='alert alert-danger py-1 px-2 mb-2 small d-flex align-items-center gap-2'>" +
        "<span class='me-auto'><b>Fault</b> on binding " + e(fault.binding) + ": " + e(fault.reason) + "</span>" +
        "<button class='btn btn-sm btn-light' data-action='clear_faults' data-id='" + id + "'>Clear</button></div>";
    });
    html += "<div class='small mb-2' data-refusal='" + id + "'></div>";
    html += DevicesPage.channelsHtml(info, drivers);
    if (!info.builtIn) {
      html += "<textarea class='form-control form-control-sm mt-2' rows='1' placeholder='Notes (where it is, what it " +
        "is wired to)' data-setting='notes' data-id='" + id + "'>" + e(info.notes) + "</textarea>";
    }
    return html + "</div></div>";
  },

  channelsHtml: function (info, drivers) {
    const e = DevicePages.escape;
    const channels = info.manifest ? info.manifest.channels || [] : [];
    if (channels.length === 0) {
      return "<div class='small text-secondary'>" + (info.manifest ? "No channels." :
        "No manifest yet: the channels show once the device announces them.") + "</div>";
    }
    let html = "";
    const inputs = channels.filter(function (channel) { return channel.dir === "in"; });
    const outputs = channels.filter(function (channel) { return channel.dir === "out"; });
    if (inputs.length > 0) {
      html += "<table class='table table-sm'><thead><tr><th style='width: 30%'>Input</th><th style='width: 15%'>Type" +
        "</th><th>Value</th><th style='width: 15%'>Updated</th></tr></thead><tbody>";
      inputs.forEach(function (channel) {
        const key = e(info.id + "/" + channel.id);
        html += "<tr><td><b>" + e(channel.id) + "</b>" + (channel.name ? " <span class='text-secondary'>" +
          e(channel.name) + "</span>" : "") + "</td><td>" + e(channel.type) + (channel.safety ? ", safety" : "") +
          "</td><td class='input-value' data-input='" + key + "'></td><td class='small' data-input-time='" + key +
          "'></td></tr>";
      });
      html += "</tbody></table>";
    }
    if (outputs.length > 0) {
      html += "<table class='table table-sm mt-2'><thead><tr><th style='width: 30%'>Output</th>" +
        "<th style='width: 15%'>Type</th><th>Now showing</th><th>Driven by</th></tr></thead><tbody>";
      outputs.forEach(function (channel) {
        const driver = drivers.find(function (candidate) { return candidate.channel === channel.id; }) || null;
        html += "<tr><td><b>" + e(channel.id) + "</b>" + (channel.name ? " <span class='text-secondary'>" +
          e(channel.name) + "</span>" : "") + "</td><td>" + e(channel.type) +
          (channel.pixels ? " · " + channel.pixels + " px" : "") + "</td><td>" +
          DevicePages.outputHtml(info.id, channel) + "</td><td class='small'>" +
          DevicesPage.driverHtml(info, channel, driver) + "</td></tr>";
        if (!info.builtIn && info.state === "approved") {
          html += "<tr><td colspan='4' class='border-0 pt-0 pb-2'>" + DevicesPage.testHtml(info, channel, driver) +
            "</td></tr>";
        }
      });
      html += "</tbody></table>";
    }
    return html;
  },

  driverHtml: function (info, channel, driver) {
    const e = DevicePages.escape;
    if (info.builtIn) {
      return "the arena";
    }
    if (driver === null) {
      return "<span class='text-secondary'>nothing</span>";
    }
    if (driver.owner === "bindings") {
      return "binding <a href='/setup/bindings#" + e(driver.binding) + "'>" + e(driver.binding) + "</a>" +
        (driver.active ? "" : " <span class='text-danger'>(has errors; see the Bindings page)</span>");
    }
    if (driver.owner === "manual test") {
      return "<span class='text-warning'>your test</span>";
    }
    return e(driver.owner);
  },

  // The controls that test an output by hand. A channel something else drives says what, instead.
  testHtml: function (info, channel, driver) {
    const e = DevicePages.escape;
    const ref = "data-id='" + e(info.id) + "' data-channel='" + e(channel.id) + "'";
    const keep = function (what) { return " data-keep='" + e(info.id + "/" + channel.id + "/" + what) + "'"; };
    if (driver !== null && driver.owner !== "manual test") {
      const what = driver.owner === "bindings" ? "binding " + driver.binding : driver.owner;
      return "<span class='small text-secondary'>To test this output by hand, free it from " + e(what) +
        " first.</span>";
    }
    let html = "<div class='test-controls'><span class='small text-secondary me-1'>Test:</span>";
    switch (channel.type) {
      case "digital":
      case "relay":
        html += "<button class='btn btn-sm btn-outline-warning' data-test='on' " + ref + ">On</button>" +
          "<button class='btn btn-sm btn-outline-secondary' data-test='off' " + ref + ">Off</button>";
        break;
      case "pwm":
        html += "<input type='range' class='form-range' min='0' max='100' value='50'" + keep("level") + ">" +
          "<button class='btn btn-sm btn-outline-warning' data-test='pwm' " + ref + ">Set level</button>";
        break;
      case "led_strip":
        html += "<select class='form-select form-select-sm w-auto'" + keep("pattern") + ">" +
          ["solid", "blink", "pulse", "chase", "rainbow", "progress", "off"].map(function (pattern) {
            return "<option>" + pattern + "</option>";
          }).join("") + "</select>" +
          "<input type='color' value='#00c853' title='First colour'" + keep("color1") + ">" +
          "<input type='color' value='#000000' title='Second colour'" + keep("color2") + ">" +
          "<input type='number' class='form-control form-control-sm' min='0' step='50' value='500' " +
          "title='Period (ms)'" + keep("ms") + ">" +
          "<input type='range' class='form-range' min='0' max='100' value='100' title='Brightness'" +
          keep("br") + ">" +
          "<button class='btn btn-sm btn-outline-warning' data-test='strip' " + ref + ">Show</button>";
        break;
      case "display":
        html += "<input type='text' class='form-control form-control-sm w-auto' placeholder='Text or number'" +
          keep("text") + "><button class='btn btn-sm btn-outline-warning' data-test='display' " + ref +
          ">Show</button>";
        break;
      case "sound":
        html += "<input type='text' class='form-control form-control-sm w-auto' placeholder='Sound name'" +
          keep("sound") + "><button class='btn btn-sm btn-outline-warning' data-test='sound' " + ref +
          ">Play</button>";
        break;
    }
    if (driver !== null && driver.owner === "manual test") {
      html += "<button class='btn btn-sm btn-outline-light ms-2' data-test='end' " + ref + ">End test</button>" +
        "<span class='small text-secondary'>Bindings and the Lighting page cannot drive it until the test ends.</span>";
    }
    return html + "</div>";
  },

  renderTransports: function (state) {
    const e = DevicePages.escape;
    let html = "<h5>Connections</h5>";
    state.transports.forEach(function (transport) {
      const badge = transport.running ? "<span class='badge text-bg-success'>running</span>" :
        "<span class='badge text-bg-secondary'>off</span>";
      html += "<div class='mb-2'><b>" + e(transport.name.toUpperCase()) + "</b> " + badge + " <span class='small'>" +
        transport.clients + " connected</span>";
      (transport.addresses || []).forEach(function (address) {
        html += "<div class='small font-monospace'>" + e(address) + "</div>";
      });
      if (transport.error) {
        html += "<div class='small text-danger'>" + e(transport.error) + "</div>";
      }
      html += "</div>";
    });
    const plc = DevicePages.device("plc");
    html += "<div class='mb-2'><b>PLC</b> " + (plc === null ? "<span class='badge text-bg-secondary'>not configured" +
      "</span>" : plc.online ? "<span class='badge text-bg-success'>answering</span>" :
      "<span class='badge text-bg-danger'>not answering</span>") + "</div>";
    if (state.mqtt.port > 0) {
      html += "<div class='small text-secondary'>Devices connect to <span class='font-monospace'>" +
        e(state.mqtt.hosts[0] || "") + ":" + state.mqtt.port + "</span>. Field profile <b>" + e(state.profile) +
        "</b>.</div>";
    } else {
      html += "<div class='small text-warning'>MQTT is turned off in Settings, so boards cannot connect over the " +
        "network.</div>";
    }
    $("#transports").html(html);
  },

  // Updates what changes all the time without redrawing the cards.
  updateLive: function (state) {
    state.devices.forEach(function (info) {
      const meta = $("[data-meta]").filter(function () { return $(this).attr("data-meta") === info.id; });
      if (meta.length > 0) {
        const status = info.status || {};
        const parts = [];
        parts.push(info.online ? "Connected " + DevicePages.ago(DevicesPage.connectedSince(info)) :
          "Last seen " + DevicePages.ago(info.lastSeen));
        if (info.online && !DevicePages.isZeroTime(info.statusTime)) {
          if (typeof status.up === "number") {
            parts.push("Up " + DevicePages.duration(status.up));
          }
          if (typeof status.rssi === "number") {
            parts.push("Wi-Fi " + status.rssi + " dBm");
          }
          if (status.ip) {
            parts.push("IP " + status.ip);
          }
          if (typeof status.heap === "number") {
            parts.push("Free memory " + Math.round(status.heap / 1024) + " kB");
          }
        }
        meta.html(parts.map(function (part) { return "<span>" + DevicePages.escape(part) + "</span>"; }).join(""));
      }
      const refusal = $("[data-refusal]").filter(function () { return $(this).attr("data-refusal") === info.id; });
      if (info.refusal) {
        const r = info.refusal;
        refusal.html("<span class='text-warning'><b>Last refusal</b> " + DevicePages.escape(DevicePages.ago(r.time)) +
          (r.count > 1 ? " (" + r.count + " times in a row)" : "") + ": " + (r.binding ? "binding " +
          DevicePages.escape(r.binding) + ": " : "") + DevicePages.escape(r.reason) + "</span>");
      } else {
        refusal.empty();
      }
      DevicesPage.updateInputs(info);
    });
  },

  connectedSince: function (info) {
    let since = null;
    Object.values(info.presence || {}).forEach(function (presence) {
      if (since === null || presence.since < since) {
        since = presence.since;
      }
    });
    return since;
  },

  updateInputs: function (info) {
    const channels = info.manifest ? info.manifest.channels || [] : [];
    channels.forEach(function (channel) {
      if (channel.dir !== "in") {
        return;
      }
      const key = info.id + "/" + channel.id;
      const input = (info.inputs || {})[channel.id];
      $("[data-input]").filter(function () { return $(this).attr("data-input") === key; })
        .html(DevicePages.formatInput(channel, input));
      $("[data-input-time]").filter(function () { return $(this).attr("data-input-time") === key; })
        .text(input ? DevicePages.ago(input.time) : "");
    });
  },

  // Shows an input as it arrives, and makes it flash.
  flash: function (event) {
    if (event.type !== "device.input") {
      return;
    }
    const info = DevicePages.device(event.data.id);
    if (info === null) {
      return;
    }
    DevicesPage.updateInputs(info);
    const key = event.data.id + "/" + event.data.channel;
    const cell = $("[data-input]").filter(function () { return $(this).attr("data-input") === key; });
    cell.removeClass("input-flash");
    void cell[0]?.offsetWidth;
    cell.addClass("input-flash");
  },

  action: function (event) {
    event.preventDefault();
    const action = $(this).attr("data-action");
    const id = $(this).attr("data-id");
    const info = DevicePages.device(id);
    const name = info ? info.name : id;
    const done = function (message) {
      return function () {
        if (message) {
          DevicePages.notify(message, "success");
        }
      };
    };
    switch (action) {
      case "approve":
        DevicePages.post("/setup/devices/" + encodeURIComponent(id) + "/approve")
          .then(done(name + " is approved. It reconnects by itself, then bind it on the Bindings page."))
          .catch(DevicePages.fail);
        return;
      case "block":
        if (confirm("Block " + name + "? It is disconnected and refused from now on" +
          (info && info.stopBound ? ", and the stops bound to it fault." : "."))) {
          DevicePages.post("/setup/devices/" + encodeURIComponent(id) + "/block").then(done()).catch(DevicePages.fail);
        }
        return;
      case "delete":
        if (confirm("Delete " + name + "? If it connects again it is a new device, waiting for approval.")) {
          DevicePages.post("/setup/devices/" + encodeURIComponent(id) + "/delete").then(done(name + " deleted."))
            .catch(DevicePages.fail);
        }
        return;
      case "rename": {
        const newName = prompt("Name for " + id + " (leave empty to use the name the device gives itself):",
          info && info.name !== id ? info.name : "");
        if (newName !== null) {
          DevicePages.post("/setup/devices/" + encodeURIComponent(id) + "/settings", {name: newName})
            .catch(DevicePages.fail);
        }
        return;
      }
      case "token":
        if (confirm("Give " + name + " a new token? The old one stops working at once: the device is disconnected " +
          "until its firmware has the new token.")) {
          DevicePages.post("/setup/devices/" + encodeURIComponent(id) + "/token").then(DevicesPage.showToken)
            .catch(DevicePages.fail);
        }
        return;
      case "identify":
        DevicePages.post("/setup/devices/" + encodeURIComponent(id) + "/identify")
          .then(done("Asked " + name + " to identify itself.")).catch(DevicePages.fail);
        return;
      case "reboot":
        if (confirm("Reboot " + name + "?")) {
          DevicePages.post("/setup/devices/" + encodeURIComponent(id) + "/reboot")
            .then(done("Asked " + name + " to reboot.")).catch(DevicePages.fail);
        }
        return;
      case "clear_faults":
        DevicePages.post("/setup/devices/" + encodeURIComponent(id) + "/clear_faults")
          .then(done("Faults of " + name + " cleared.")).catch(DevicePages.fail);
        return;
      case "clear_all_faults":
        DevicePages.post("/setup/devices/clear_faults").then(done("Faults cleared.")).catch(DevicePages.fail);
        return;
      case "unblock":
        DevicePages.post("/setup/devices/" + encodeURIComponent(id) + "/unblock")
          .then(done(name + " is pending again; approve it when it connects.")).catch(DevicePages.fail);
        return;
    }
  },

  setting: function () {
    const id = $(this).attr("data-id");
    const body = {};
    body[$(this).attr("data-setting")] = $(this).is(":checkbox") ? $(this).prop("checked") : $(this).val();
    DevicePages.post("/setup/devices/" + encodeURIComponent(id) + "/settings", body).catch(DevicePages.fail);
  },

  testOutput: function () {
    const id = $(this).attr("data-id");
    const channel = $(this).attr("data-channel");
    const url = "/setup/devices/" + encodeURIComponent(id) + "/outputs/" + encodeURIComponent(channel);
    const value = function (what) {
      return $("[data-keep]").filter(function () {
        return $(this).attr("data-keep") === id + "/" + channel + "/" + what;
      }).val();
    };
    let command = null;
    switch ($(this).attr("data-test")) {
      case "on":
        command = {v: 1};
        break;
      case "off":
        command = {v: 0};
        break;
      case "pwm":
        command = {v: Number(value("level")) / 100};
        break;
      case "strip": {
        const pattern = value("pattern");
        command = {p: pattern, c: [value("color1"), value("color2")], br: Number(value("br")) / 100};
        if (["blink", "pulse", "chase", "rainbow"].includes(pattern)) {
          command.ms = Math.max(0, parseInt(value("ms")) || 0);
        }
        if (pattern === "progress") {
          command.v = 0.5;
        }
        break;
      }
      case "display": {
        const text = value("text") || "";
        command = text.trim() !== "" && !isNaN(Number(text)) ? {v: Number(text)} : {text: text};
        break;
      }
      case "sound":
        command = {play: value("sound") || ""};
        break;
      case "end":
        DevicePages.post(url + "/end_test").catch(DevicePages.fail);
        return;
    }
    DevicePages.post(url + "/test", command).catch(DevicePages.fail);
  },

  // Shows a token, once, with a snippet for each SDK.
  showToken: function (data) {
    const info = data.device;
    DevicesPage.token = {token: data.token, id: info.id, name: info.name};
    $("#tokenTitle").text("Token for " + info.name);
    $("#tokenValue").text(data.token);
    const hosts = data.mqtt.hosts || [];
    $("#tokenHosts").empty().append(hosts.map(function (host) { return $("<option>").attr("value", host); }));
    $("#tokenHost").val(hosts[0] || "");
    $("#tokenPort").val(data.mqtt.port || 1883);
    $("#tokenMqttOff").toggleClass("d-none", data.mqtt.port > 0);
    DevicesPage.renderSnippets();
    $("#tokenModal").modal("show");
  },

  renderSnippets: function () {
    const token = DevicesPage.token;
    if (token === null) {
      return;
    }
    const host = ($("#tokenHost").val() || "").trim();
    const port = parseInt($("#tokenPort").val()) || 1883;
    const quote = function (text) { return JSON.stringify(String(text)); };
    $("#snippetArduino").text([
      "#include <Galaxia.h>",
      "",
      "GalaxiaDevice dev(" + quote(token.id) + ", " + quote(token.name) + ");",
      "// Declare the channels here, for example:",
      "// Counter count = dev.counterPin(\"count\", 27);",
      "",
      "void setup() {",
      "  dev.wifi(\"your-wifi-name\", \"your-wifi-password\");",
      "  dev.connect(" + quote(host) + ", " + quote(token.token) + ", " + port + ");",
      "}",
      "",
      "void loop() {",
      "  dev.loop();",
      "}",
    ].join("\n"));
    $("#snippetMicropython").text([
      "import galaxia",
      "",
      "dev = galaxia.Device(" + quote(token.id) + ", " + quote(token.name) + ")",
      "# Declare the channels here, for example:",
      "# count = dev.counter_pin(\"count\", 15)",
      "",
      "dev.wifi(\"your-wifi-name\", \"your-wifi-password\")",
      "dev.connect(" + quote(host) + ", " + quote(token.token) + ", port=" + port + ")",
      "dev.run()",
    ].join("\n"));
    $("#snippetPython").text([
      "from galaxia import Device",
      "",
      "dev = Device(" + quote(token.id) + ", " + quote(token.name) + ")",
      "# Declare the channels here, for example:",
      "# count = dev.counter(\"count\")",
      "",
      "dev.connect(" + quote(host) + ", " + quote(token.token) + ", port=" + port + ")",
      "dev.run_forever()",
    ].join("\n"));
    $("#snippetCli").text("galaxia-fms device sim --broker " + host + ":" + port + " --id " + token.id + " --token " +
      token.token + " --manifest manifest.json");
  },
};

$(function () {
  if ($("#devicesPage").length > 0) {
    DevicesPage.start();
  }
});
