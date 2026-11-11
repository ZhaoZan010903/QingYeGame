import { Plus } from "lucide-react";
import { makeCode, parseFilters, type EditableGroup } from "../lib/admin-ux";
export function CategoryFields({
  value,
  onChange,
}: {
  value: string;
  onChange: (v: string) => void;
}) {
  let groups: EditableGroup[];
  try {
    groups = parseFilters(value);
  } catch {
    return (
      <p className="form-error" role="alert">
        现有筛选资料格式异常，已保留原内容。请在下方高级配置中检查。
      </p>
    );
  }
  const commit = (next: EditableGroup[]) =>
    onChange(JSON.stringify(next, null, 2));
  const update = (index: number, patch: Partial<EditableGroup>) =>
    commit(groups.map((g, i) => (i === index ? { ...g, ...patch } : g)));
  return (
    <section>
      <div className="section-heading">
        <h3>用户可以筛选什么？</h3>
        <span>例如区服、性别、账号类型</span>
      </div>
      <p className="field-hint">
        修改名称不会改变已有商品关联。不再使用的分组或选项，关闭“显示”即可。
      </p>
      {groups.map((group, index) => (
        <div className="subform-card" key={group.key}>
          <div className="admin-grid">
            <label className="form-field">
              筛选分组名称
              <input
                value={group.label}
                maxLength={40}
                required
                onChange={(e) => update(index, { label: e.target.value })}
              />
            </label>
            <label className="form-field">
              选择方式
              <select
                value={group.multiple ? "multiple" : "single"}
                onChange={(e) =>
                  update(index, { multiple: e.target.value === "multiple" })
                }
              >
                <option value="single">单选：只能选择一个</option>
                <option value="multiple">多选：可以同时选择多个</option>
              </select>
            </label>
          </div>
          <label className="agreement-check">
            <input
              type="checkbox"
              checked={group.enabled !== false}
              onChange={(e) => update(index, { enabled: e.target.checked })}
            />
            在前台显示这个分组
          </label>
          <div className="filter-options">
            {group.options.map((option, oi) => (
              <div className="filter-option-row" key={option.value}>
                <label className="form-field">
                  选项 {oi + 1}
                  <input
                    aria-label={`${group.label}选项 ${oi + 1}`}
                    required
                    maxLength={80}
                    value={option.label}
                    onChange={(e) =>
                      update(index, {
                        options: group.options.map((o, i) =>
                          i === oi ? { ...o, label: e.target.value } : o,
                        ),
                      })
                    }
                  />
                </label>
                <label className="agreement-check">
                  <input
                    type="checkbox"
                    checked={option.enabled !== false}
                    onChange={(e) =>
                      update(index, {
                        options: group.options.map((o, i) =>
                          i === oi ? { ...o, enabled: e.target.checked } : o,
                        ),
                      })
                    }
                  />
                  显示
                </label>
              </div>
            ))}
          </div>
          <button
            type="button"
            className="text-button"
            disabled={group.options.length >= 200}
            onClick={() =>
              update(index, {
                options: [
                  ...group.options,
                  { value: makeCode("option"), label: "新选项", enabled: true },
                ],
              })
            }
          >
            <Plus size={14} />
            添加选项
          </button>
        </div>
      ))}
      {!groups.length && (
        <p className="admin-empty">
          还没有筛选分组，可以添加“区服”“性别”等选项。
        </p>
      )}
      <button
        type="button"
        className="button-outline"
        disabled={groups.length >= 30}
        onClick={() =>
          commit([
            ...groups,
            {
              key: makeCode("filter"),
              label: "新筛选分组",
              multiple: false,
              enabled: true,
              options: [],
            },
          ])
        }
      >
        <Plus size={16} />
        添加筛选分组
      </button>
    </section>
  );
}
