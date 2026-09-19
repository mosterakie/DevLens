// 自定义下拉菜单。
//
// 原生 <select> 的弹出层由操作系统绘制，无法用 CSS 统一风格，
// 在明亮主题下尤其突兀。这里用 div 重做一个，同时保留键盘操作
// 和 ARIA 语义，避免为了外观牺牲可用性。
//
// 用法：
//   DevLensSelect.create({ mount, options, value, onChange, ariaLabel })

(function () {
  let openInstance = null;

  // 点击外部或按 Esc 关闭当前打开的下拉。
  document.addEventListener('click', () => { if (openInstance) openInstance.close(); });
  document.addEventListener('keydown', (e) => {
    if (e.key === 'Escape' && openInstance) {
      openInstance.close();
      openInstance.trigger.focus();
    }
  });

  function create(opts) {
    const { mount, options, onChange, ariaLabel } = opts;

    const root = document.createElement('div');
    root.className = 'select';

    const trigger = document.createElement('button');
    trigger.type = 'button';
    trigger.setAttribute('aria-haspopup', 'listbox');
    trigger.setAttribute('aria-expanded', 'false');
    if (ariaLabel) trigger.setAttribute('aria-label', ariaLabel);

    const label = document.createElement('span');
    trigger.appendChild(label);

    const caret = document.createElement('span');
    caret.className = 'caret';
    caret.setAttribute('aria-hidden', 'true');
    trigger.appendChild(caret);

    const menu = document.createElement('div');
    menu.className = 'menu';
    menu.setAttribute('role', 'listbox');

    root.appendChild(trigger);
    root.appendChild(menu);
    mount.appendChild(root);

    let value = opts.value;
    let items = options.slice();

    function renderMenu() {
      menu.innerHTML = '';
      items.forEach((opt) => {
        const btn = document.createElement('button');
        btn.type = 'button';
        btn.setAttribute('role', 'option');
        btn.setAttribute('aria-selected', String(opt.value === value));
        btn.textContent = opt.label;
        btn.addEventListener('click', (e) => {
          e.stopPropagation();
          select(opt.value);
          close();
          trigger.focus();
        });
        menu.appendChild(btn);
      });
    }

    function renderTrigger() {
      const found = items.find((o) => o.value === value);
      label.textContent = found ? found.label : '';
    }

    function open() {
      if (openInstance && openInstance !== instance) openInstance.close();
      root.classList.add('open');
      trigger.setAttribute('aria-expanded', 'true');
      openInstance = instance;
      const selected = menu.querySelector('[aria-selected="true"]');
      if (selected) selected.focus();
    }

    function close() {
      root.classList.remove('open');
      trigger.setAttribute('aria-expanded', 'false');
      if (openInstance === instance) openInstance = null;
    }

    function select(next) {
      if (next === value) return;
      value = next;
      renderTrigger();
      renderMenu();
      if (onChange) onChange(next);
    }

    // 键盘操作：上下键在选项间移动，Enter/Space 选择。
    menu.addEventListener('keydown', (e) => {
      const buttons = Array.from(menu.querySelectorAll('button'));
      const idx = buttons.indexOf(document.activeElement);
      if (e.key === 'ArrowDown') {
        e.preventDefault();
        buttons[Math.min(idx + 1, buttons.length - 1)]?.focus();
      } else if (e.key === 'ArrowUp') {
        e.preventDefault();
        buttons[Math.max(idx - 1, 0)]?.focus();
      } else if (e.key === 'Home') {
        e.preventDefault();
        buttons[0]?.focus();
      } else if (e.key === 'End') {
        e.preventDefault();
        buttons[buttons.length - 1]?.focus();
      }
    });

    trigger.addEventListener('click', (e) => {
      e.stopPropagation();
      if (root.classList.contains('open')) close();
      else open();
    });

    renderTrigger();
    renderMenu();

    const instance = {
      root, trigger, close, open,
      getValue: () => value,
      setValue: (v) => select(v),
      // setOptions 用于语言切换后重绘标签。
      setOptions: (next) => {
        items = next.slice();
        renderTrigger();
        renderMenu();
      },
    };
    return instance;
  }

  window.DevLensSelect = { create };
})();