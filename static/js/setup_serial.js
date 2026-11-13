// Client-side logic for the serial device page: lists the computer's serial ports, opens or closes each for field
// devices, and shows how the open ones are doing.

var serialRefreshMs = 2000;
var serialBusy = false; // A change is on its way: the list is not redrawn under the operator's hand meanwhile.
var serialChangeError = ""; // Why the last change was refused; it stays until the next change.

var showSerialError = function (text) {
  $("#serialError").text(text).toggleClass("d-none", !text);
};

// describeSerialState says in words how a chosen port is doing.
var describeSerialState = function (port) {
  var state = port.state;
  if (!port.chosen) {
    return "";
  }
  if (!state || !state.open) {
    return "Not open" + (state && state.problem ? ": " + state.problem : "");
  }
  var text = state.device ? "Open: device " + state.device + " (" + state.access + ")" :
    "Open, waiting for a device's manifest";
  if (state.problem) {
    text += ". Last problem: " + state.problem;
  }
  return text;
};

var renderSerialPorts = function (view) {
  $("#serialNotRunning").toggleClass("d-none", view.running);
  showSerialError(serialChangeError || (view.listError ? "The ports could not be listed: " + view.listError : ""));
  var body = $("#serialPorts").empty();
  if (view.ports.length === 0) {
    body.append($("<tr>").append($("<td colspan='5'>").text("The computer has no serial ports.")));
    return;
  }
  view.ports.forEach(function (port) {
    var checkbox = $("<input type='checkbox' class='form-check-input serial-open'>")
      .prop("checked", port.chosen)
      .attr("aria-label", "Open " + port.name + " for devices")
      .on("change", function () {
        setSerialPort(port.name, this.checked);
      });
    var row = $("<tr>").append(
      $("<td>").append(checkbox),
      $("<td>").text(port.name + (port.present ? "" : " (not plugged in)")),
      $("<td>").text(port.vid ? port.vid + ":" + port.pid : ""),
      $("<td>").text(port.product || port.manufacturer || ""),
      $("<td>").text(describeSerialState(port))
    );
    if (port.chosen && !(port.state && port.state.open)) {
      row.addClass("table-warning");
    }
    body.append(row);
  });
};

var loadSerialPorts = function () {
  if (serialBusy) {
    return;
  }
  $.getJSON("/setup/devices/serial/ports").done(renderSerialPorts).fail(function (xhr) {
    showSerialError("The ports could not be loaded (" + (xhr.statusText || "no answer") + ").");
  });
};

// setSerialPort opens or closes a port for devices.
var setSerialPort = function (name, open) {
  serialBusy = true;
  $(".serial-open").prop("disabled", true);
  $.ajax({
    url: "/setup/devices/serial/ports",
    method: "POST",
    contentType: "application/json",
    dataType: "json",
    data: JSON.stringify({port: name, open: open})
  }).done(function (view) {
    serialChangeError = "";
    renderSerialPorts(view);
  }).fail(function (xhr) {
    var answer = xhr.responseJSON || {};
    serialChangeError = answer.error || "The change could not be made (" + (xhr.statusText || "no answer") + ").";
    if (answer.ports) {
      renderSerialPorts(answer);
    } else {
      showSerialError(serialChangeError);
    }
  }).always(function () {
    serialBusy = false;
    loadSerialPorts();
  });
};

$(function () {
  $("#serialAddForm").on("submit", function (event) {
    event.preventDefault();
    var name = $("#serialAddPort").val().trim();
    if (name) {
      $("#serialAddPort").val("");
      setSerialPort(name, true);
    }
  });
  loadSerialPorts();
  setInterval(loadSerialPorts, serialRefreshMs);
});
