// Leaflet maps (SPEC §14). Progressive enhancement: without this file (or Leaflet) the map
// containers stay hidden and pages still show the village list, coordinates and inputs.
// Tiles come from this server (/tiles/...), so the CSP stays 'self'.
(function () {
  "use strict";

  // Matches geo.Indonesia on the server; detailed tiles exist only inside it.
  var INDONESIA = [[-11.5, 94.0], [6.5, 141.5]];
  var CENTER = [-2.5, 118.0];

  function escapeHTML(s) {
    return String(s).replace(/[&<>"']/g, function (c) {
      return { "&": "&amp;", "<": "&lt;", ">": "&gt;", '"': "&quot;", "'": "&#39;" }[c];
    });
  }

  function num(s) {
    if (s == null) return NaN;
    s = String(s).trim().replace(",", ".");
    return s === "" ? NaN : Number(s);
  }

  function inIndonesia(lat, lng) {
    return lat >= INDONESIA[0][0] && lat <= INDONESIA[1][0] && lng >= INDONESIA[0][1] && lng <= INDONESIA[1][1];
  }

  function createMap(el) {
    var d = el.dataset;
    var maxZoom = parseInt(d.maxZoom, 10) || 17;
    el.classList.remove("hidden");
    var map = L.map(el, {
      zoomControl: false,
      minZoom: 4,
      maxZoom: maxZoom,
      maxBounds: L.latLngBounds(INDONESIA).pad(0.15),
      maxBoundsViscosity: 0.8,
    });
    L.control.zoom({ zoomInTitle: d.zoomIn, zoomOutTitle: d.zoomOut }).addTo(map);
    var credit = '<a href="' + escapeHTML(d.attributionUrl) + '" rel="noopener noreferrer" target="_blank">' + escapeHTML(d.attribution) + "</a>";
    L.tileLayer(d.tiles, { maxZoom: maxZoom, attribution: credit }).addTo(map);
    return map;
  }

  function marker(lat, lng, name, opts) {
    opts = opts || {};
    opts.alt = name;
    opts.title = name;
    return L.marker([lat, lng], opts);
  }

  var init = {
    // A single village.
    point: function (el) {
      var lat = num(el.dataset.lat), lng = num(el.dataset.lng);
      if (isNaN(lat) || isNaN(lng)) return;
      var map = createMap(el);
      map.setView([lat, lng], 14);
      marker(lat, lng, el.dataset.name).addTo(map);
    },

    // Every village in a list: each <li> carries data-lat/lng/name/place/href.
    list: function (el) {
      var list = document.getElementById(el.dataset.list);
      if (!list) return;
      var map = createMap(el);
      var points = [];
      list.querySelectorAll("[data-lat]").forEach(function (li) {
        var lat = num(li.dataset.lat), lng = num(li.dataset.lng);
        if (isNaN(lat) || isNaN(lng)) return;
        // Built from DOM nodes, so village names are never parsed as HTML.
        var box = document.createElement("div");
        var title = document.createElement("strong");
        title.textContent = li.dataset.name;
        var place = document.createElement("p");
        place.textContent = li.dataset.place;
        var link = document.createElement("a");
        link.href = li.dataset.href;
        link.textContent = el.dataset.openLabel;
        box.append(title, place, link);
        marker(lat, lng, li.dataset.name).bindPopup(box).addTo(map);
        points.push([lat, lng]);
      });
      if (points.length === 0) map.setView(CENTER, 4);
      else if (points.length === 1) map.setView(points[0], 12);
      else map.fitBounds(points, { padding: [30, 30], maxZoom: 12 });
    },

    // Location picker bound to two text inputs; the inputs stay the source of truth.
    picker: function (el) {
      var latIn = document.getElementById(el.dataset.latInput);
      var lngIn = document.getElementById(el.dataset.lngInput);
      if (!latIn || !lngIn) return;
      var map = createMap(el);
      var pin = null;

      function place(lat, lng) {
        if (pin) pin.setLatLng([lat, lng]);
        else {
          pin = marker(lat, lng, el.getAttribute("aria-label"), { draggable: true }).addTo(map);
          pin.on("dragend", function () {
            var p = pin.getLatLng();
            write(p.lat, p.lng);
          });
        }
      }
      function write(lat, lng) {
        latIn.value = lat.toFixed(6);
        lngIn.value = lng.toFixed(6);
      }
      function fromInputs(recenter) {
        var lat = num(latIn.value), lng = num(lngIn.value);
        if (isNaN(lat) || isNaN(lng) || !inIndonesia(lat, lng)) return false;
        place(lat, lng);
        if (recenter) map.setView([lat, lng], Math.max(map.getZoom() || 0, 14));
        return true;
      }

      if (!fromInputs(true)) map.setView(CENTER, 4);
      map.on("click", function (e) {
        place(e.latlng.lat, e.latlng.lng);
        write(e.latlng.lat, e.latlng.lng);
      });
      latIn.addEventListener("change", function () { fromInputs(true); });
      lngIn.addEventListener("change", function () { fromInputs(true); });
    },
  };

  document.addEventListener("DOMContentLoaded", function () {
    if (!window.L) return;
    L.Icon.Default.imagePath = "/static/leaflet/images/";
    document.querySelectorAll("[data-map]").forEach(function (el) {
      var fn = init[el.dataset.map];
      if (fn) fn(el);
    });
  });
})();
