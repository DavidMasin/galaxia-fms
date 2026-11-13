// Client-side logic for the virtual devices page: making devices from templates or a pasted manifest, clicking their
// inputs, and drawing their outputs. The devices belong to the FMS; this page only drives them.

// Kinds of device to start from. Each is an ordinary manifest; the page adds nothing a real board could not send.
const VIRTUAL_TEMPLATES = [
  {
    label: "Button", id: "virtual-button", name: "Virtual button",
    channels: [{id: "button", dir: "in", type: "digital", name: "Button"}],
  },
  {
    label: "Beam-break counter", id: "virtual-counter", name: "Virtual beam break",
    channels: [
      {id: "count", dir: "in", type: "counter", name: "Count"},
      {id: "beam", dir: "in", type: "digital", name: "Beam broken"},
    ],
  },
  {
    label: "E-stop box", id: "virtual-estop", name: "Virtual E-stop box",
    channels: [
      {id: "stop", dir: "in", type: "digital", safety: true, name: "E-stop"},
      {id: "light", dir: "out", type: "digital", name: "Indicator"},
    ],
  },
  {
    label: "Analog sensor", id: "virtual-sensor", name: "Virtual analog sensor",
    channels: [{id: "level", dir: "in", type: "analog", name: "Level", unit: "kg", min: 0, max: 40}],
  },
  {
    label: "RFID reader", id: "virtual-rfid", name: "Virtual RFID reader",
    channels: [{id: "tag", dir: "in", type: "text", name: "Tag"}],
  },
  {
    label: "LED strip (30 px)", id: "virtual-strip", name: "Virtual LED strip",
    channels: [{id: "strip", dir: "out", type: "led_strip", pixels: 30, name: "Strip"}],
  },
  {
    label: "Stack light (4 relays)", id: "virtual-stack", name: "Virtual stack light",
    channels: [
      {id: "red", dir: "out", type: "relay", name: "Red lamp"},
      {id: "amber", dir: "out", type: "relay", name: "Amber lamp"},
      {id: "green", dir: "out", type: "relay", name: "Green lamp"},
      {id: "buzzer", dir: "out", type: "relay", name: "Buzzer"},
    ],
  },
];

const VirtualPage = {
  template: null,         // The template being created.
  signatures: {},
  queue: Promise.resolve(), // Inputs are sent one after the other, so that a press arrives before its release.

  start: function () {
    const templates = $("#templates");
    VIRTUAL_TEMPLATES.forEach(function (template, index) {
      templates.append($("<button type='button' class='btn btn-outline-primary template-button'>")
        .text(template.label).attr("data-template", index));
    });
    templates.on("click", "[data-template]", function () {
      VirtualPage.openCreate(VIRTUAL_TEMPLATES[parseInt($(this).attr("data-template"))]);
    });
    $("#createForm").on("submit", function (event) {
      event.preventDefault();
      const manifest = {
        proto: 1, id: $("#createId").val().trim(), name: $("#createName").val().trim(),
        channels: VirtualPage.template.channels,
      };
      VirtualPage.create(manifest).then(function () {
        $("#createModal").modal("hide");
      }).catch(function (error) {
        $("#createError").removeClass("d-none").text(error.message);
      });
    });
    $("#createFromJson").on("click", function () {
      let manifest;
      try {
        manifest = JSON.parse($("#manifestJson").val());
      } catch (e) {
        DevicePages.notify("That is not JSON: " + e.message, "danger");
        return;
      }
      VirtualPage.create(manifest).catch(DevicePages.fail);
    });

    const container = $("#virtualDevices");
    container.on("click", "[data-virtual]", VirtualPage.action);
    container.on("click", "[data-press]", VirtualPage.press);
    container.on("pointerdown", "[data-hold]", function (event) {
      event.preventDefault();
      $(this).addClass("active").data("held", true);
      VirtualPage.input($(this).attr("data-id"), {channel: $(this).attr("data-hold"), value: 1});
    });
    container.on("pointerup pointerleave pointercancel", "[data-hold]", function () {
      if ($(this).data("held")) {
        $(this).removeClass("active").data("held", false);
        VirtualPage.input($(this).attr("data-id"), {channel: $(this).attr("data-hold"), value: 0});
      }
    });
    container.on("click", "[data-add]", function () {
      VirtualPage.input($(this).attr("data-id"), {channel: $(this).attr("data-channel"),
        add: Number($(this).attr("data-add"))});
    });
    container.on("input", "[data-analog]", VirtualPage.analogChanged);
    container.on("click", "[data-send]", function () {
      const field = $(this).closest(".control-row").find("[data-value]");
      const channel = DevicePages.channel($(this).attr("data-id"), $(this).attr("data-send"));
      let value = field.val();
      if (channel && channel.type !== "text") {
        value = Number(value);
      }
      VirtualPage.input($(this).attr("data-id"), {channel: $(this).attr("data-send"), value: value});
    });
    container.on("keydown", "[data-value]", function (event) {
      if (event.key === "Enter") {
        $(this).closest(".control-row").find("[data-send]").trigger("click");
      }
    });
    container.on("click", "[data-estop]", function () {
      const stopped = $(this).hasClass("latched");
      VirtualPage.input($(this).attr("data-id"), {channel: $(this).attr("data-estop"), value: stopped ? 1 : 0});
    });
    container.on("change", "[data-heartbeat]", function () {
      DevicePages.post(VirtualPage.url($(this).attr("data-heartbeat"), "heartbeat"), {on: $(this).prop("checked")})
        .catch(DevicePages.fail);
    });

    DevicePages.onChange(VirtualPage.render);
    DevicePages.connect();
  },

  url: function (id, action) {
    return "/setup/devices/virtual/" + encodeURIComponent(id) + "/" + action;
  },

  // A free id for a new device made from a template: virtual-button-1, virtual-button-2, ...
  freeId: function (base) {
    const taken = DevicePages.devices().map(function (info) { return info.id; });
    for (let n = 1; ; n++) {
      if (!taken.includes(base + "-" + n)) {
        return base + "-" + n;
      }
    }
  },

  openCreate: function (template) {
    VirtualPage.template = template;
    const id = VirtualPage.freeId(template.id);
    $("#createTitle").text("New " + template.label.toLowerCase());
    $("#createId").val(id);
    $("#createName").val(template.name + " " + id.split("-").pop());
    $("#createChannels").text("Channels: " + DevicePages.channelSummary(template));
    $("#createError").addClass("d-none");
    $("#createModal").modal("show");
  },

  create: function (manifest) {
    return DevicePages.post("/setup/devices/virtual/create", {manifest: manifest}).then(function (data) {
      const pending = data.device && data.device.state === "pending";
      DevicePages.notify("Created " + manifest.id + "." + (pending ? " Approve it below before its inputs count." : ""),
        "success");
      return data;
    });
  },

  // Sends an input, after every input sent before it.
  input: function (id, body) {
    VirtualPage.queue = VirtualPage.queue.then(function () {
      return DevicePages.post(VirtualPage.url(id, "input"), body);
    }).catch(DevicePages.fail);
    return VirtualPage.queue;
  },

  // A short press: on, then off a moment later, long enough for a debounced binding to see it.
  press: function () {
    const id = $(this).attr("data-id");
    const channel = $(this).attr("data-press");
    VirtualPage.input(id, {channel: channel, value: 1});
    VirtualPage.queue = VirtualPage.queue.then(function () {
      return new Promise(function (resolve) { setTimeout(resolve, 150); });
    });
    VirtualPage.input(id, {channel: channel, value: 0});
  },

  analogChanged: function () {
    const slider = $(this);
    const id = slider.attr("data-id");
    const channel = slider.attr("data-analog");
    slider.closest(".control-row").find(".analog-value").text(slider.val());
    // Sent at most ten times a second while the slider moves, and once more where it stops.
    clearTimeout(slider.data("timer"));
    const now = Date.now();
    const send = function () {
      slider.data("sent", Date.now());
      VirtualPage.input(id, {channel: channel, value: Number(slider.val())});
    };
    if (now - (slider.data("sent") || 0) > 100) {
      send();
    } else {
      slider.data("timer", setTimeout(send, 100));
    }
  },

  action: function () {
    const id = $(this).attr("data-id");
    const action = $(this).attr("data-virtual");
    let request;
    switch (action) {
      case "approve":
        request = DevicePages.post("/setup/devices/" + encodeURIComponent(id) + "/approve");
        break;
      case "remove":
        if (!confirm("Remove " + id + "? It is disconnected and forgotten.")) {
          return;
        }
        request = DevicePages.post(VirtualPage.url(id, "remove"));
        break;
      default:
        request = DevicePages.post(VirtualPage.url(id, action));
    }
    request.catch(DevicePages.fail);
  },

  render: function (state) {
    const container = $("#virtualDevices");
    if (state.virtual.length === 0) {
      container.html("<div class='text-secondary'>No virtual devices yet. Pick a template above.</div>");
      VirtualPage.signatures = {};
      return;
    }
    container.children(":not(.virtual-card)").remove();
    const ids = state.virtual.map(function (view) { return view.id; });
    container.children(".virtual-card").each(function () {
      if (!ids.includes($(this).attr("data-card"))) {
        delete VirtualPage.signatures[$(this).attr("data-card")];
        $(this).remove();
      }
    });
    state.virtual.forEach(function (view, index) {
      const info = DevicePages.device(view.id);
      const signature = JSON.stringify([view.connected, view.beating, view.manifest, info ? info.state : "",
        info ? info.name : "", info ? info.stopBound : false]);
      let card = container.children(".virtual-card").filter(function () {
        return $(this).attr("data-card") === view.id;
      });
      if (card.length === 0 || VirtualPage.signatures[view.id] !== signature) {
        const fresh = $(VirtualPage.cardHtml(view, info));
        if (card.length > 0) {
          card.replaceWith(fresh);
        } else if (index === 0) {
          container.prepend(fresh);
        } else {
          fresh.insertAfter(container.children(".virtual-card").eq(index - 1));
        }
        card = fresh;
        VirtualPage.signatures[view.id] = signature;
      }
      VirtualPage.updateValues(card, view, info);
    });
  },

  cardHtml: function (view, info) {
    const e = DevicePages.escape;
    const id = e(view.id);
    const name = info ? info.name : (view.manifest && view.manifest.name) || view.id;
    const state = info ? info.state : "unknown";
    let html = "<div class='card virtual-card mb-3' data-card='" + id + "'><div class='card-header d-flex " +
      "align-items-center gap-2 flex-wrap'><b>" + e(name) + "</b> <code>" + id + "</code>";
    html += view.connected ? " <span class='badge text-bg-success'>connected</span>" :
      " <span class='badge text-bg-secondary'>switched off</span>";
    if (state === "approved") {
      html += " <span class='badge text-bg-success'>approved</span>";
    } else if (state === "pending") {
      html += " <span class='badge text-bg-warning'>waiting for approval</span>";
    } else if (state === "blocked") {
      html += " <span class='badge text-bg-danger'>blocked</span>";
    }
    if (info && info.stopBound) {
      html += " <span class='badge text-bg-warning'>bound to a stop</span>";
    }
    html += "<span class='ms-auto'></span>";
    if (view.connected) {
      html += "<button class='btn btn-sm btn-outline-warning' data-virtual='disconnect' data-id='" + id + "' " +
        "title='As if its power were cut'>Switch off</button>";
    } else {
      html += "<button class='btn btn-sm btn-outline-success' data-virtual='connect' data-id='" + id +
        "'>Switch on</button>";
    }
    html += "<button class='btn btn-sm btn-outline-danger' data-virtual='remove' data-id='" + id + "'>Remove</button>" +
      "</div><div class='card-body'>";
    if (state === "pending") {
      html += "<div class='alert alert-warning d-flex align-items-center gap-3 py-2'><span class='me-auto'>Like any " +
        "new device, this one is waiting for approval: until then the FMS ignores its inputs.</span>" +
        "<button class='btn btn-success' data-virtual='approve' data-id='" + id + "'>Approve</button></div>";
    }
    const channels = view.manifest ? view.manifest.channels || [] : [];
    const hasSafety = channels.some(function (channel) { return channel.safety; });
    if (hasSafety && view.connected) {
      html += "<div class='form-check form-switch mb-2'><input class='form-check-input' type='checkbox' id='hb-" + id +
        "' data-heartbeat='" + id + "'" + (view.beating ? " checked" : "") +
        "><label class='form-check-label' for='hb-" + id + "'>Heartbeat every 200 ms</label> " +
        "<span class='small text-secondary'>Switch it off to see the watchdog stop the field and latch a fault " +
        "within a second.</span></div>";
    }
    channels.filter(function (channel) { return channel.dir === "in"; }).forEach(function (channel) {
      html += "<div class='control-row'><span class='control-label'>" + e(channel.name || channel.id) +
        " <span class='small text-secondary fw-normal'>" + e(channel.id) + "</span></span>" +
        VirtualPage.controlHtml(view, channel) + "<span class='small text-secondary ms-2'>FMS has: <span " +
        "class='input-value' data-fms='" + e(channel.id) + "'></span></span></div>";
    });
    const outputs = channels.filter(function (channel) { return channel.dir === "out"; });
    if (outputs.length > 0) {
      html += "<hr><div class='small text-secondary mb-1'>Outputs, as the device shows them</div>";
      outputs.forEach(function (channel) {
        html += "<div class='control-row'><span class='control-label'>" + e(channel.name || channel.id) +
          " <span class='small text-secondary fw-normal'>" + e(channel.id) + "</span></span>" +
          DevicePages.outputHtml(view.id, channel, "virtual").replace("class='lamp'", "class='lamp lamp-large'") +
          "</div>";
      });
    }
    html += "<div class='small text-secondary mt-2' data-extra='" + id + "'></div>";
    return html + "</div></div>";
  },

  controlHtml: function (view, channel) {
    const e = DevicePages.escape;
    const ref = " data-id='" + e(view.id) + "'";
    const disabled = view.connected ? "" : " disabled";
    switch (channel.type) {
      case "digital":
        if (channel.safety) {
          return "<button class='estop-button' data-estop='" + e(channel.id) + "'" + ref + disabled +
            ">STOP</button><span class='small' data-safety='" + e(channel.id) + "'></span>";
        }
        return "<button class='btn btn-outline-warning' data-press='" + e(channel.id) + "'" + ref + disabled +
          ">Press</button><button class='btn btn-outline-warning btn-hold' data-hold='" + e(channel.id) + "'" + ref +
          disabled + ">Hold</button><span class='input-value' data-own='" + e(channel.id) + "'></span>";
      case "counter":
        return "<button class='btn btn-outline-warning' data-add='1' data-channel='" + e(channel.id) + "'" + ref +
          disabled + ">+1</button><button class='btn btn-outline-warning' data-add='5' data-channel='" +
          e(channel.id) + "'" + ref + disabled + ">+5</button><span class='input-value'>total <span data-own='" +
          e(channel.id) + "'></span></span>";
      case "analog": {
        const min = typeof channel.min === "number" ? channel.min : 0;
        const max = typeof channel.max === "number" ? channel.max : 100;
        return "<input type='range' class='form-range' style='width: 16rem' min='" + min + "' max='" + max +
          "' step='" + ((max - min) / 200) + "' value='" + min + "' data-analog='" + e(channel.id) + "'" + ref +
          disabled + "><span class='input-value analog-value'>" + min + "</span> " + e(channel.unit || "");
      }
      case "encoder":
        return "<input type='number' class='form-control form-control-sm' style='width: 8rem' step='1' value='0' " +
          "data-value" + disabled + "><button class='btn btn-sm btn-outline-warning' data-send='" + e(channel.id) +
          "'" + ref + disabled + ">Send</button>";
      case "text":
        return "<input type='text' class='form-control form-control-sm' style='width: 12rem' placeholder='04a1b2c3' " +
          "data-value" + disabled + "><button class='btn btn-sm btn-outline-warning' data-send='" + e(channel.id) +
          "'" + ref + disabled + ">Read</button>";
    }
    return "";
  },

  updateValues: function (card, view, info) {
    const channels = view.manifest ? view.manifest.channels || [] : [];
    channels.forEach(function (channel) {
      if (channel.dir !== "in") {
        return;
      }
      const own = view.values ? view.values[channel.id] : undefined;
      card.find("[data-own]").filter(function () { return $(this).attr("data-own") === channel.id; })
        .text(own === undefined ? (channel.type === "counter" ? "0" : "") : String(own));
      if (channel.safety) {
        const stopped = own === 0;
        card.find("[data-estop]").filter(function () { return $(this).attr("data-estop") === channel.id; })
          .toggleClass("latched", stopped).text(stopped ? "RESET" : "STOP")
          .attr("title", stopped ? "Pressed: click to release" : "Click to press");
        card.find("[data-safety]").filter(function () { return $(this).attr("data-safety") === channel.id; })
          .html(stopped ? "<b class='text-danger'>stopped</b> (circuit open)" : "safe (circuit closed)");
      }
      const input = info && info.inputs ? info.inputs[channel.id] : null;
      card.find("[data-fms]").filter(function () { return $(this).attr("data-fms") === channel.id; })
        .html(input ? DevicePages.formatInput(channel, input) : "nothing yet");
    });
    const extra = [];
    if (view.commands && view.commands.length > 0) {
      extra.push("Commands received: " + view.commands.map(function (command) { return command.do; }).join(", "));
    }
    if (view.field) {
      extra.push("Field: " + view.field.state + (view.field.phase ? ", " + view.field.phase + " " +
        Math.round(view.field.phase_remaining) + " s left" : ""));
    }
    if (info && info.refusal) {
      extra.push("Last refusal: " + info.refusal.reason);
    }
    card.find("[data-extra]").text(extra.join(" · "));
  },
};

$(function () {
  VirtualPage.start();
});
