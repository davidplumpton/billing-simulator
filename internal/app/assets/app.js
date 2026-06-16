(function () {
	"use strict";

	var fragmentHeader = "X-Billing-Simulator-Fragment";
	var debounceTimers = new WeakMap();

	function closestPartialForm(element) {
		if (!element || !element.closest) {
			return null;
		}
		return element.closest("form[data-partial-form]");
	}

	function formURL(form) {
		var url = new URL(form.getAttribute("action") || window.location.href, window.location.href);
		var data = new FormData(form);
		url.search = "";
		data.forEach(function (value, key) {
			if (typeof value === "string" && value.trim() === "") {
				return;
			}
			url.searchParams.append(key, value);
		});
		return url;
	}

	function requestFormSubmit(form) {
		if (form.requestSubmit) {
			form.requestSubmit();
			return;
		}
		form.dispatchEvent(new Event("submit", { bubbles: true, cancelable: true }));
	}

	function scheduleFormSubmit(form) {
		window.clearTimeout(debounceTimers.get(form));
		debounceTimers.set(form, window.setTimeout(function () {
			requestFormSubmit(form);
		}, 250));
	}

	function refreshPartial(form, target) {
		var fragment = form.getAttribute("data-partial-form");
		var url = formURL(form);
		target.setAttribute("aria-busy", "true");
		form.classList.add("is-refreshing");

		var headers = { "X-Requested-With": "fetch" };
		headers[fragmentHeader] = fragment;

		window.fetch(url.toString(), {
			credentials: "same-origin",
			headers: headers
		}).then(function (response) {
			if (!response.ok) {
				throw new Error("fragment refresh failed");
			}
			return response.text();
		}).then(function (html) {
			target.innerHTML = html;
			window.history.replaceState(null, "", url.toString());
		}).catch(function () {
			window.location.assign(url.toString());
		}).finally(function () {
			target.removeAttribute("aria-busy");
			form.classList.remove("is-refreshing");
		});
	}

	document.addEventListener("submit", function (event) {
		var form = closestPartialForm(event.target);
		if (!form || String(form.method).toLowerCase() !== "get" || !window.fetch || !window.FormData || !window.URL) {
			return;
		}
		var target = document.querySelector(form.getAttribute("data-partial-target"));
		if (!target) {
			return;
		}
		event.preventDefault();
		refreshPartial(form, target);
	});

	document.addEventListener("input", function (event) {
		var form = closestPartialForm(event.target);
		if (!form || form.getAttribute("data-partial-auto") !== "true") {
			return;
		}
		scheduleFormSubmit(form);
	});

	document.addEventListener("change", function (event) {
		var form = closestPartialForm(event.target);
		if (!form || form.getAttribute("data-partial-auto") !== "true") {
			return;
		}
		requestFormSubmit(form);
	});
}());
