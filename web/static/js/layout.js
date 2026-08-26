// Shell compartilhado do dashboard (sidebar + topbar), injetado ao redor de
// #page-content em toda página do Console (E7.S2, ADR 0019). Alternativa
// zero-build a um include/templating: cada página carrega este script e
// mantém apenas seu <main id="page-content"> próprio.
(function () {
  "use strict";

  var NAV_ITEMS = [
    { href: "/", label: "Início", icon: "🏠" },
    { href: "/componentes/index.html", label: "Componentes", icon: "📦" },
    { href: "/componentes/novo.html", label: "Novo Componente", icon: "➕" },
  ];

  function currentPath() {
    var path = window.location.pathname;
    if (path.length > 1 && path.endsWith("/")) {
      path = path.slice(0, -1);
    }
    return path || "/";
  }

  function buildSidebar() {
    var aside = document.createElement("aside");
    aside.className = "kf-sidebar";

    var brand = document.createElement("div");
    brand.className = "kf-sidebar-brand";
    brand.innerHTML = "⚙️ <span>KubeForge</span>";
    aside.appendChild(brand);

    var nav = document.createElement("nav");
    nav.className = "kf-sidebar-nav";

    var path = currentPath();
    NAV_ITEMS.forEach(function (item) {
      var a = document.createElement("a");
      a.href = item.href;
      a.innerHTML = item.icon + " <span>" + item.label + "</span>";
      if (item.href === path) {
        a.classList.add("active");
        a.setAttribute("aria-current", "page");
      }
      nav.appendChild(a);
    });
    aside.appendChild(nav);

    return aside;
  }

  function buildBackdrop(shell) {
    var backdrop = document.createElement("div");
    backdrop.className = "kf-sidebar-backdrop";
    backdrop.addEventListener("click", function () {
      shell.classList.remove("kf-mobile-open");
    });
    return backdrop;
  }

  function buildTopbar(shell) {
    var header = document.createElement("header");
    header.className = "kf-topbar";

    var toggle = document.createElement("button");
    toggle.type = "button";
    toggle.className = "kf-topbar-toggle";
    toggle.setAttribute("aria-label", "Alternar menu lateral");
    toggle.textContent = "☰";
    toggle.addEventListener("click", function () {
      if (window.matchMedia("(max-width: 767.98px)").matches) {
        shell.classList.toggle("kf-mobile-open");
      } else {
        shell.classList.toggle("kf-collapsed");
      }
    });
    header.appendChild(toggle);

    var title = document.createElement("span");
    title.className = "fw-semibold";
    title.textContent = "Console";
    header.appendChild(title);

    return header;
  }

  document.addEventListener("DOMContentLoaded", function () {
    var shell = document.getElementById("app-shell");
    var pageContent = document.getElementById("page-content");
    if (!shell || !pageContent) {
      return;
    }

    var sidebar = buildSidebar();
    var backdrop = buildBackdrop(shell);
    var main = document.createElement("div");
    main.className = "kf-main";

    var topbar = buildTopbar(shell);
    main.appendChild(topbar);

    pageContent.classList.add("kf-page-content");
    // appendChild move o nó existente (em vez de cloná-lo), preservando a
    // identidade de qualquer elemento dentro dele — importante porque
    // scripts da própria página (ex.: componente-form.js) podem prender
    // listeners a esses elementos antes ou depois deste ponto.
    main.appendChild(pageContent);

    shell.textContent = "";
    shell.appendChild(sidebar);
    shell.appendChild(backdrop);
    shell.appendChild(main);
    shell.style.visibility = "visible";
  });
})();
