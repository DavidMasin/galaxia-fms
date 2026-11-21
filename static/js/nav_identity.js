// Shows who is logged in on the navigation bar, with a way to log out, while logins are on. While they are off the
// bar stays as it is.

$(function () {
  $.getJSON("/api/v1/auth").done(function (auth) {
    if (!auth.required) {
      return;
    }
    if (auth.identity) {
      $("#navIdentityName").text(auth.identity.name + " (" + auth.identity.role + ")");
      $("#navIdentity, #navLogout").removeClass("d-none");
    } else {
      // Come back to this page after logging in.
      const here = location.pathname + location.search;
      $("#navLogin a").attr("href", "/login?redirect=" + encodeURIComponent(here));
      $("#navLogin").removeClass("d-none");
    }
  });
});
