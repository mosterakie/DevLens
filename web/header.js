// 页头在四个页面里一致，所以集中在一处生成。
//
// 用 JS 渲染而不是复制四份 HTML：语言切换需要重建导航文案，
// 复制四份必然出现某一页漏改。

(function () {
  const I = window.DevLensI18n;

  function currentPage() {
    const p = location.pathname.split('/').pop() || 'index.html';
    if (p === '' || p === 'index.html') return 'home';
    if (p === 'analyze.html') return 'analyze';
    if (p === 'incidents.html' || p === 'detail.html') return 'incidents';
    return '';
  }

  function build() {
    const host = document.getElementById('site-header');
    if (!host) return;

    const page = currentPage();

    const logo = document.createElement('a');
    logo.className = 'logo';
    logo.href = 'index.html';
    logo.textContent = 'DevLens';
    logo.style.color = 'inherit';

    const nav = document.createElement('nav');

    const links = [
      { key: 'nav.home', href: 'index.html', id: 'home' },
      { key: 'nav.analyze', href: 'analyze.html', id: 'analyze' },
      { key: 'nav.incidents', href: 'incidents.html', id: 'incidents' },
    ];

    links.forEach((l) => {
      const a = document.createElement('a');
      a.href = l.href;
      a.dataset.i18n = l.key;
      a.textContent = I.t(l.key);
      if (page === l.id) a.classList.add('active');
      nav.appendChild(a);
    });

    // 语言切换
    const langMount = document.createElement('span');
    nav.appendChild(langMount);

    host.innerHTML = '';
    host.appendChild(logo);
    host.appendChild(nav);

    const langSelect = window.DevLensSelect.create({
      mount: langMount,
      value: I.getLang(),
      ariaLabel: I.t('nav.lang'),
      options: I.supported.map((l) => ({ value: l, label: I.displayName(l) })),
      onChange: (v) => I.setLang(v),
    });

    // 语言变化时刷新导航文案和下拉标签。
    I.onLangChange(() => {
      nav.querySelectorAll('[data-i18n]').forEach((el) => {
        el.textContent = I.t(el.dataset.i18n);
      });
      langSelect.setOptions(
        I.supported.map((l) => ({ value: l, label: I.displayName(l) }))
      );
      langSelect.trigger.setAttribute('aria-label', I.t('nav.lang'));
    });
  }

  window.DevLensHeader = { build };
})();