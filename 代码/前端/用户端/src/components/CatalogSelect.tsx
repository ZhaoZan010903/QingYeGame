import { useEffect, useId, useLayoutEffect, useRef, useState, type KeyboardEvent } from 'react';
import { Check, ChevronDown } from 'lucide-react';
import '../catalog-select.css';

export type CatalogSelectOption = {
  value: string;
  label: string;
  disabled?: boolean;
  description?: string;
};

export function CatalogSelect({
  label,
  value,
  onChange,
  options,
  prefix = false,
  className = '',
}: {
  label: string;
  value: string;
  onChange: (value: string) => void;
  options: CatalogSelectOption[];
  prefix?: boolean;
  className?: string;
}) {
  const id = useId();
  const listId = `${id}-options`;
  const wrapper = useRef<HTMLDivElement>(null);
  const trigger = useRef<HTMLButtonElement>(null);
  const menu = useRef<HTMLUListElement>(null);
  const [open, setOpen] = useState(false);
  const [active, setActive] = useState(-1);
  const [placement, setPlacement] = useState({ above: false, maxHeight: 280 });
  const selected = options.findIndex((option) => option.value === value);
  const current = options[selected];
  const enabled = options.flatMap((option, index) => (option.disabled ? [] : [index]));

  const close = () => setOpen(false);
  const reveal = (index = selected) => {
    setActive(enabled.includes(index) ? index : (enabled[0] ?? -1));
    setOpen(true);
  };
  const choose = (index: number) => {
    const option = options[index];
    if (!option || option.disabled) return;
    onChange(option.value);
    close();
    trigger.current?.focus({ preventScroll: true });
  };

  useEffect(() => {
    if (!open) return;
    const outside = (event: PointerEvent) => {
      if (!wrapper.current?.contains(event.target as Node)) close();
    };
    document.addEventListener('pointerdown', outside);
    return () => document.removeEventListener('pointerdown', outside);
  }, [open]);

  useLayoutEffect(() => {
    if (!open) return;
    const measure = () => {
      const box = trigger.current?.getBoundingClientRect();
      if (!box) return;
      // Keep the menu inside the native dialog's top layer when used in mobile filters.
      const dialog = wrapper.current?.closest('dialog')?.getBoundingClientRect();
      const top = Math.max(8, dialog ? dialog.top + 8 : 8);
      const bottom = Math.min(window.innerHeight - 8, dialog ? dialog.bottom - 8 : Infinity);
      const below = bottom - box.bottom - 7;
      const above = box.top - top - 7;
      const useAbove = below < 220 && above > below;
      const maxHeight = Math.max(80, Math.min(280, useAbove ? above : below));
      setPlacement((old) =>
        old.above === useAbove && old.maxHeight === maxHeight
          ? old
          : { above: useAbove, maxHeight },
      );
    };
    measure();
    window.addEventListener('resize', measure);
    document.addEventListener('scroll', measure, true);
    return () => {
      window.removeEventListener('resize', measure);
      document.removeEventListener('scroll', measure, true);
    };
  }, [open]);

  useLayoutEffect(() => {
    if (!open || active < 0) return;
    const list = menu.current;
    const option = list?.children[active] as HTMLElement | undefined;
    if (!list || !option) return;
    if (option.offsetTop < list.scrollTop) list.scrollTop = option.offsetTop;
    if (option.offsetTop + option.offsetHeight > list.scrollTop + list.clientHeight) {
      list.scrollTop = option.offsetTop + option.offsetHeight - list.clientHeight;
    }
  }, [open, active, placement.maxHeight]);

  const onKeyDown = (event: KeyboardEvent<HTMLButtonElement>) => {
    if (event.ctrlKey || event.metaKey || event.altKey) return;
    switch (event.key) {
      case 'ArrowDown':
      case 'ArrowUp': {
        event.preventDefault();
        if (!open) {
          reveal();
        } else {
          const index = enabled.indexOf(active);
          const direction = event.key === 'ArrowDown' ? 1 : -1;
          const next = (index + direction + enabled.length) % enabled.length;
          setActive(enabled[next] ?? -1);
        }
        break;
      }
      case 'Home':
      case 'End':
        event.preventDefault();
        reveal(event.key === 'Home' ? enabled[0] : enabled[enabled.length - 1]);
        break;
      case 'Enter':
      case ' ':
        event.preventDefault();
        if (open) choose(active);
        else reveal();
        break;
      case 'Escape':
        if (open) {
          event.preventDefault();
          event.stopPropagation();
          close();
        }
        break;
      case 'Tab':
        close();
        break;
      default: {
        if (event.key.length !== 1) return;
        const query = event.key.toLocaleLowerCase();
        const start = open ? active : selected;
        const matching = [
          ...enabled.filter((index) => index > start),
          ...enabled.filter((index) => index <= start),
        ].find((index) => options[index].label.toLocaleLowerCase().startsWith(query));
        if (matching !== undefined) {
          event.preventDefault();
          reveal(matching);
        }
      }
    }
  };

  return (
    <div
      ref={wrapper}
      className={`catalog-select ${open ? 'is-open' : ''} ${className}`}
      onBlur={(event) => {
        if (!event.currentTarget.contains(event.relatedTarget as Node | null)) close();
      }}
    >
      <button
        ref={trigger}
        type="button"
        role="combobox"
        className="catalog-select-trigger"
        aria-label={`${label}：${current?.label ?? '请选择'}`}
        aria-haspopup="listbox"
        aria-expanded={open}
        aria-controls={open ? listId : undefined}
        aria-activedescendant={open && active >= 0 ? `${id}-option-${active}` : undefined}
        onClick={() => (open ? close() : reveal())}
        onKeyDown={onKeyDown}
      >
        <span className="catalog-select-value">
          {prefix && <span className="catalog-select-prefix">{label}：</span>}
          <span>{current?.label ?? '请选择'}</span>
        </span>
        <ChevronDown aria-hidden="true" />
      </button>
      {open && (
        <ul
          ref={menu}
          id={listId}
          role="listbox"
          aria-label={label}
          className={`catalog-select-menu ${placement.above ? 'opens-above' : ''}`}
          style={{ maxHeight: placement.maxHeight }}
          onMouseDown={(event) => event.preventDefault()}
        >
          {options.map((option, index) => (
            <li
              key={option.value}
              id={`${id}-option-${index}`}
              role="option"
              aria-selected={option.value === value}
              aria-disabled={option.disabled || undefined}
              aria-describedby={option.description ? `${id}-hint-${index}` : undefined}
              className={`catalog-select-option ${active === index ? 'is-active' : ''}`}
              onPointerMove={() => !option.disabled && setActive(index)}
              onClick={() => choose(index)}
            >
              <span className="catalog-select-option-copy">
                <span>{option.label}</span>
                {option.description && (
                  <small id={`${id}-hint-${index}`}>{option.description}</small>
                )}
              </span>
              {option.value === value && <Check aria-hidden="true" />}
            </li>
          ))}
        </ul>
      )}
    </div>
  );
}
