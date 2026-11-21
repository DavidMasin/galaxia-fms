// Client-side logic for the Webhooks page and a webhook's delivery log. Both are drawn from the webhook API, and
// everything a person or a receiver wrote (names, addresses, answers) is put on the page as text, never as HTML.

const WebhooksPage = (function () {
  const REFRESH_MS = 3000;
  const LOG_PAGE = 50;

  // Sends a request to the webhook API. The promise is resolved with the answer, or rejected with a message.
  const api = function (method, path, body) {
    const deferred = $.Deferred();
    $.ajax({
      method: method,
      url: "/api/v1/webhooks" + path,
      data: body === undefined ? undefined : JSON.stringify(body),
      contentType: body === undefined ? undefined : "application/json",
      dataType: method === "DELETE" ? "text" : "json",
    }).done(function (answer) {
      deferred.resolve(answer);
    }).fail(function (xhr) {
      let message = "The FMS did not answer.";
      if (xhr.responseJSON && xhr.responseJSON.error) {
        message = xhr.responseJSON.error;
      } else if (xhr.status) {
        message = "The FMS answered " + xhr.status + ".";
      }
      deferred.reject(message);
    });
    return deferred.promise();
  };

  const element = function (tag, classes, text) {
    const created = $(document.createElement(tag));
    if (classes) {
      created.addClass(classes);
    }
    if (text !== undefined && text !== null) {
      created.text(text);
    }
    return created;
  };

  const showMessage = function (kind, text) {
    const alert = element("div", "alert alert-dismissible alert-" + kind, text);
    alert.append($('<button type="button" class="btn-close" data-bs-dismiss="alert" aria-label="Close"></button>'));
    $("#webhookMessages").empty().append(alert);
  };

  const showError = function (message) {
    showMessage("danger", message);
  };

  const pad = function (number) {
    return String(number).padStart(2, "0");
  };

  // "14:03:22" today, "11-20 14:03:22" on another day.
  const formatTime = function (iso) {
    if (!iso) {
      return "";
    }
    const time = new Date(iso);
    const clock = pad(time.getHours()) + ":" + pad(time.getMinutes()) + ":" + pad(time.getSeconds());
    if (time.toDateString() === new Date().toDateString()) {
      return clock;
    }
    return pad(time.getMonth() + 1) + "-" + pad(time.getDate()) + " " + clock;
  };

  // "in 40 s", "in 12 min", "in 3 h", or "now".
  const formatUntil = function (iso) {
    if (!iso) {
      return "";
    }
    const seconds = Math.round((new Date(iso).getTime() - Date.now()) / 1000);
    if (seconds <= 0) {
      return "now";
    }
    if (seconds < 90) {
      return "in " + seconds + " s";
    }
    if (seconds < 5400) {
      return "in " + Math.round(seconds / 60) + " min";
    }
    return "in " + Math.round(seconds / 3600) + " h";
  };

  const STATUS = {
    pending: {label: "Waiting", badge: "text-bg-warning"},
    sending: {label: "Sending", badge: "text-bg-info"},
    succeeded: {label: "Delivered", badge: "text-bg-success"},
    failed: {label: "Failed", badge: "text-bg-danger"},
  };

  const statusBadge = function (delivery) {
    const status = STATUS[delivery.status] || {label: delivery.status, badge: "text-bg-secondary"};
    let label = status.label;
    if (delivery.status === "pending" && delivery.attempts > 0) {
      label = "Retrying";
    }
    return element("span", "badge " + status.badge, label);
  };

  // What the receiver said last: "200", "500", or "no answer".
  const answerText = function (delivery) {
    if (delivery.attempts === 0) {
      return "";
    }
    return delivery.responseCode ? String(delivery.responseCode) : "no answer";
  };

  // ---------------------------------------------------------------------------------------------------------------
  // The list of webhooks.

  let listState = {webhooks: [], eventTypes: [], defaultEvents: []};
  let editing = null; // The webhook in the form, or null for a new one.
  let modal = null;

  const lastDeliveryLine = function (webhook) {
    const line = element("div", "small text-secondary");
    const last = webhook.lastDelivery;
    if (!last) {
      return line.text("Nothing sent yet.");
    }
    line.append(element("span", "me-2", "Last:"), statusBadge(last));
    let detail = " " + last.event + ", " + formatTime(last.lastAttemptAt || last.createdAt);
    const answer = answerText(last);
    if (answer) {
      detail += ", answered " + answer + " in " + last.latencyMs + " ms";
    }
    line.append(document.createTextNode(detail));
    if (last.error && last.status !== "succeeded") {
      line.append(element("div", "text-danger-emphasis", last.error));
    }
    return line;
  };

  const webhookCard = function (webhook) {
    const card = element("div", "card bg-body-tertiary mb-3 webhook-card");
    const body = element("div", "card-body").appendTo(card);
    const header = element("div", "d-flex align-items-center flex-wrap gap-2").appendTo(body);
    header.append(element("h5", "mb-0", webhook.name));
    header.append(element("span", "badge " + (webhook.enabled ? "text-bg-success" : "text-bg-secondary"),
      webhook.enabled ? "Enabled" : "Disabled"));
    if (webhook.pending > 0) {
      header.append(element("span", "badge text-bg-warning", webhook.pending + " waiting"));
    }
    const buttons = element("div", "ms-auto d-flex flex-wrap gap-2").appendTo(header);
    element("button", "btn btn-sm btn-outline-primary", "Send a test event").attr("type", "button")
      .on("click", function () {
        api("POST", "/" + webhook.id + "/test").done(function () {
          showMessage("success", "A test event is on its way to " + webhook.name + ". Its result shows below.");
          refreshList();
        }).fail(showError);
      }).appendTo(buttons);
    element("a", "btn btn-sm btn-outline-secondary", "Deliveries").attr("href", "/setup/webhooks/" + webhook.id)
      .appendTo(buttons);
    element("button", "btn btn-sm btn-outline-secondary", "Edit").attr("type", "button")
      .on("click", function () {
        openForm(webhook.id);
      }).appendTo(buttons);

    body.append(element("div", "font-monospace small text-break mt-2", webhook.url));
    const events = element("div", "mt-2 d-flex flex-wrap gap-1").appendTo(body);
    webhook.events.forEach(function (event) {
      events.append(element("span", "badge text-bg-dark border font-monospace", event === "*" ? "every event" : event));
    });
    body.append(lastDeliveryLine(webhook).addClass("mt-2"));
    return card;
  };

  const drawList = function () {
    const list = $("#webhookList").empty();
    if (listState.webhooks.length === 0) {
      list.append(element("div", "card card-body bg-body-tertiary text-secondary",
        "No webhooks yet. Add one to send events to a program of yours."));
    }
    listState.webhooks.forEach(function (webhook) {
      list.append(webhookCard(webhook));
    });
    const depth = listState.outboxDepth || 0;
    $("#outboxDepth").text(depth + " waiting to be sent").toggleClass("d-none", depth === 0);
  };

  const refreshList = function () {
    return api("GET", "").done(function (answer) {
      listState = answer;
      drawList();
    });
  };

  // The checkboxes of the known event types, by group.
  const drawEventChoices = function (selected) {
    const container = $("#webhookEvents").empty();
    const known = {};
    let group = null;
    let row = null;
    listState.eventTypes.forEach(function (eventType, index) {
      known[eventType.type] = true;
      if (eventType.group !== group) {
        group = eventType.group;
        container.append(element("div", "webhook-event-group", group));
        row = element("div", "row g-1").appendTo(container);
      }
      const id = "webhookEvent" + index;
      const check = element("div", "form-check col-md-6").appendTo(row);
      $('<input class="form-check-input" type="checkbox">').attr("id", id).attr("data-event", eventType.type)
        .prop("checked", selected.indexOf(eventType.type) >= 0).appendTo(check);
      const label = element("label", "form-check-label").attr("for", id).appendTo(check);
      label.append(element("span", "font-monospace", eventType.type));
      if (eventType.highRate) {
        label.append(element("span", "badge text-bg-warning ms-1", "many a second"));
      }
      if (eventType.planned) {
        label.append(element("span", "badge text-bg-secondary ms-1", "not sent yet"));
      }
      label.append(element("div", "small text-secondary", eventType.description));
    });
    const every = selected.length === 1 && selected[0] === "*";
    $("#webhookEveryEvent").prop("checked", every);
    $("#webhookOtherEvents").val(selected.filter(function (event) {
      return event !== "*" && !known[event];
    }).join(", "));
    showEventChoices();
  };

  const showEventChoices = function () {
    const every = $("#webhookEveryEvent").prop("checked");
    $("#webhookEvents input, #webhookOtherEvents").prop("disabled", every);
    $("#webhookEvents").toggleClass("opacity-50", every);
  };

  const chosenEvents = function () {
    if ($("#webhookEveryEvent").prop("checked")) {
      return ["*"];
    }
    const events = $("#webhookEvents input:checked").map(function () {
      return $(this).attr("data-event");
    }).get();
    $("#webhookOtherEvents").val().split(",").forEach(function (event) {
      event = event.trim();
      if (event && events.indexOf(event) < 0) {
        events.push(event);
      }
    });
    return events;
  };

  const fillForm = function (webhook) {
    editing = webhook;
    $("#webhookFormError").addClass("d-none");
    $("#webhookModalTitle").text(webhook ? "Edit " + webhook.name : "Add webhook");
    $("#webhookUrl").val(webhook ? webhook.url : "");
    $("#webhookName").val(webhook ? webhook.name : "");
    $("#webhookEnabled").prop("checked", webhook ? webhook.enabled : true);
    $("#webhookSecretRow").toggleClass("d-none", !webhook);
    $("#webhookSecret").val(webhook ? webhook.secret : "").attr("type", "password");
    $("#showSecret").text("Show");
    $("#deleteWebhook").toggleClass("d-none", !webhook);
    drawEventChoices(webhook ? webhook.events : listState.defaultEvents);
  };

  // Opens the form for a new webhook, or for an existing one, read again so that its secret is there.
  const openForm = function (id) {
    if (!id) {
      fillForm(null);
      modal.show();
      return;
    }
    api("GET", "/" + id).done(function (webhook) {
      fillForm(webhook);
      modal.show();
    }).fail(showError);
  };

  const formError = function (message) {
    $("#webhookFormError").text(message).removeClass("d-none");
  };

  const saveForm = function () {
    const changes = {
      url: $("#webhookUrl").val().trim(),
      name: $("#webhookName").val().trim(),
      events: chosenEvents(),
      enabled: $("#webhookEnabled").prop("checked"),
    };
    const wasNew = !editing;
    const request = wasNew ? api("POST", "", changes) : api("PUT", "/" + editing.id, changes);
    request.done(function (webhook) {
      refreshList();
      if (wasNew) {
        // Stay on the form, now showing the secret the receiving program needs.
        fillForm(webhook);
        $("#webhookSecret").attr("type", "text");
        $("#showSecret").text("Hide");
        $("#webhookModalTitle").text(webhook.name + " was added. Copy its secret into the receiving program.");
      } else {
        modal.hide();
      }
    }).fail(formError);
  };

  const startList = function () {
    modal = new bootstrap.Modal(document.getElementById("webhookModal"));
    $("#addWebhook").on("click", function () {
      openForm(0);
    });
    $("#webhookForm").on("submit", function (event) {
      event.preventDefault();
      saveForm();
    });
    $("#webhookEveryEvent").on("change", showEventChoices);
    $("#showSecret").on("click", function () {
      const hidden = $("#webhookSecret").attr("type") === "password";
      $("#webhookSecret").attr("type", hidden ? "text" : "password");
      $(this).text(hidden ? "Hide" : "Show");
    });
    $("#copySecret").on("click", function () {
      const button = $(this);
      navigator.clipboard.writeText($("#webhookSecret").val()).then(function () {
        button.text("Copied");
      }, function () {
        formError("The browser would not copy it. Show the secret and copy it by hand.");
      });
    });
    $("#newSecret").on("click", function () {
      if (!editing || !confirm("Make a new secret? The receiving program must be given it, or it will refuse " +
        "every delivery from now on.")) {
        return;
      }
      api("POST", "/" + editing.id + "/secret").done(function (webhook) {
        editing = webhook;
        $("#webhookSecret").val(webhook.secret).attr("type", "text");
        $("#showSecret").text("Hide");
      }).fail(formError);
    });
    $("#deleteWebhook").on("click", function () {
      if (!editing || !confirm("Delete " + editing.name + ", with what is waiting for it and its delivery log?")) {
        return;
      }
      api("DELETE", "/" + editing.id).done(function () {
        modal.hide();
        refreshList();
      }).fail(formError);
    });
    refreshList().fail(showError);
    setInterval(function () {
      // Not while the form is open: redrawing would not disturb it, but the list behind it need not move.
      if (!$("#webhookModal").hasClass("show")) {
        refreshList();
      }
    }, REFRESH_MS);
  };

  // ---------------------------------------------------------------------------------------------------------------
  // One webhook's delivery log.

  let logWebhook = 0;
  let logOffset = 0;
  let logTotal = 0;
  const openRows = {}; // Delivery ids whose details are shown.

  const prettyJson = function (text) {
    try {
      return JSON.stringify(JSON.parse(text), null, 2);
    } catch (error) {
      return text;
    }
  };

  const detailBlock = function (title, text) {
    const block = element("div", "mb-2");
    block.append(element("div", "small text-secondary", title));
    block.append(element("pre", "webhook-pre mb-0", text));
    return block;
  };

  const deliveryDetails = function (delivery) {
    const cell = element("td").attr("colspan", 8);
    const row = element("tr", "webhook-details").append(cell);
    cell.append(element("div", "small text-secondary mb-2", "Delivery " + delivery.id +
      (delivery.manual ? " (a test or a resend)" : "") +
      (delivery.deadline ? ". Retried until " + formatTime(delivery.deadline) + "." : "")));
    if (delivery.error) {
      cell.append(element("div", "text-danger-emphasis mb-2", delivery.error));
    }
    if (delivery.history.length > 0) {
      const attempts = element("ol", "small mb-2");
      delivery.history.forEach(function (attempt) {
        attempts.append(element("li", null, formatTime(attempt.time) + ": " +
          (attempt.responseCode ? "answered " + attempt.responseCode : "no answer") + " in " + attempt.latencyMs +
          " ms" + (attempt.error ? " (" + attempt.error + ")" : "")));
      });
      cell.append(element("div", "small text-secondary", "Attempts (the last ten)"), attempts);
    }
    cell.append(detailBlock("What was sent", prettyJson(delivery.body)));
    if (delivery.responseBody) {
      cell.append(detailBlock("The receiver's last answer (its start)", delivery.responseBody));
    }
    return row;
  };

  const deliveryRows = function (delivery) {
    const row = element("tr", "webhook-row");
    row.append(element("td", "text-nowrap", formatTime(delivery.createdAt)));
    row.append(element("td", "font-monospace", delivery.event));
    row.append(element("td").append(statusBadge(delivery)));
    row.append(element("td", "text-end", String(delivery.attempts)));
    row.append(element("td", "text-end", answerText(delivery)));
    row.append(element("td", "text-end", delivery.attempts ? delivery.latencyMs + " ms" : ""));
    row.append(element("td", "text-nowrap", delivery.status === "pending" ? formatUntil(delivery.nextAttemptAt) : ""));
    const actions = element("td", "text-end text-nowrap").appendTo(row);
    element("button", "btn btn-sm btn-outline-secondary me-1", openRows[delivery.id] ? "Hide" : "Details")
      .attr("type", "button").on("click", function () {
        openRows[delivery.id] = !openRows[delivery.id];
        refreshLog();
      }).appendTo(actions);
    element("button", "btn btn-sm btn-outline-primary", "Resend").attr("type", "button")
      .prop("disabled", delivery.status === "sending").on("click", function () {
        api("POST", "/" + logWebhook + "/deliveries/" + encodeURIComponent(delivery.id) + "/resend")
          .done(refreshLog).fail(showError);
      }).appendTo(actions);
    return openRows[delivery.id] ? [row, deliveryDetails(delivery)] : [row];
  };

  const refreshLog = function () {
    let query = "?offset=" + logOffset + "&limit=" + LOG_PAGE;
    const status = $("#logStatus").val();
    if (status) {
      query += "&status=" + status;
    }
    return api("GET", "/" + logWebhook + "/deliveries" + query).done(function (answer) {
      logTotal = answer.total;
      const rows = $("#logRows").empty();
      answer.deliveries.forEach(function (delivery) {
        rows.append(deliveryRows(delivery));
      });
      if (answer.deliveries.length === 0) {
        rows.append($("<tr></tr>").append(element("td", "text-secondary", "Nothing here yet.").attr("colspan", 8)));
      }
      const first = logTotal === 0 ? 0 : logOffset + 1;
      $("#logCount").text(first + " to " + (logOffset + answer.deliveries.length) + " of " + logTotal +
        ". The log keeps the latest 500 finished deliveries.");
      $("#logNewer").prop("disabled", logOffset === 0);
      $("#logOlder").prop("disabled", logOffset + LOG_PAGE >= logTotal);
    });
  };

  const startLog = function (webhookId) {
    logWebhook = webhookId;
    api("GET", "/" + webhookId).done(function (webhook) {
      $("#logTitle").text("Deliveries to " + webhook.name);
      $("#logSubtitle").text(webhook.url + (webhook.enabled ? "" : " (disabled: nothing new is queued)"));
    }).fail(showError);
    $("#logStatus").on("change", function () {
      logOffset = 0;
      refreshLog();
    });
    $("#logNewer").on("click", function () {
      logOffset = Math.max(0, logOffset - LOG_PAGE);
      refreshLog();
    });
    $("#logOlder").on("click", function () {
      logOffset += LOG_PAGE;
      refreshLog();
    });
    $("#logSendTest").on("click", function () {
      api("POST", "/" + webhookId + "/test").done(function () {
        logOffset = 0;
        refreshLog();
      }).fail(showError);
    });
    refreshLog().fail(showError);
    setInterval(refreshLog, REFRESH_MS);
  };

  return {startList: startList, startLog: startLog};
})();
