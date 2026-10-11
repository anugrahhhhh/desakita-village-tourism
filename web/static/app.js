// Progressive enhancement only; every page works without this file.
(function () {
  "use strict";

  var rupiah = function (n) {
    return "Rp " + Math.round(n).toString().replace(/\B(?=(\d{3})+(?!\d))/g, ".");
  };

  // Booking form: live estimate of the total. Display only; the server recomputes the
  // total from the database price when the request is submitted.
  function initTotals(root) {
    root.querySelectorAll("[data-total-for]").forEach(function (out) {
      var input = document.getElementById(out.getAttribute("data-total-for"));
      var price = parseInt(out.getAttribute("data-price"), 10);
      if (!input || isNaN(price)) return;
      var update = function () {
        var n = parseInt(input.value, 10);
        out.textContent = n > 0 && n <= 1000 ? rupiah(price * n) : "–";
      };
      input.addEventListener("input", update);
      update();
    });
  }

  // Menus built on <details data-dropdown>: close on Escape or a click outside. Without
  // JavaScript they still open and close with their own toggle.
  function initDropdowns() {
    var menus = document.querySelectorAll("details[data-dropdown]");
    document.addEventListener("click", function (e) {
      menus.forEach(function (d) { if (d.open && !d.contains(e.target)) d.open = false; });
    });
    document.addEventListener("keydown", function (e) {
      if (e.key !== "Escape") return;
      menus.forEach(function (d) {
        if (d.open) { d.open = false; d.querySelector("summary").focus(); }
      });
    });
  }

  document.addEventListener("DOMContentLoaded", function () { initTotals(document); initDropdowns(); });
  // Re-run after htmx swaps (e.g. calendar month navigation keeps the form intact).
  document.addEventListener("htmx:afterSwap", function (e) { initTotals(e.target); });
})();
