import type { RentalQuote } from "./types";
import {
  ordinaryRentalUnits,
  periodLabel,
  rentalQuoteLabel,
  rentalUnitLabels,
  rentalUnits,
} from "./rental-quotes";
import "./rental-quotes.css";

export function RentalQuoteEditor({
  quotes,
  onChange,
  allowed = rentalUnits,
}: {
  quotes: RentalQuote[];
  onChange: (quotes: RentalQuote[]) => void;
  allowed?: readonly string[];
}) {
  const update = (index: number, patch: Partial<RentalQuote>) =>
    onChange(quotes.map((q, i) => (i === index ? { ...q, ...patch } : q)));
  const remove = (index: number) =>
    onChange(quotes.filter((_, i) => i !== index));
  const prioritize = (index: number) =>
    onChange([quotes[index], ...quotes.filter((_, i) => i !== index)]);
  return (
    <div className="rental-quotes-form">
      {quotes.map(
        (quote, index) =>
          !allowed.includes(quote.unit) && (
            <p key={index} className="form-error">
              {rentalQuoteLabel(quote)}报价已不适用于当前分类，金额仍保留。
              <button
                type="button"
                className="text-button"
                onClick={() => remove(index)}
              >
                移除此报价
              </button>
            </p>
          ),
      )}
      <div className="rental-quote-ordinary">
        {ordinaryRentalUnits
          .filter((unit) => allowed.includes(unit))
          .map((unit) => {
            const index = quotes.findIndex((q) => q.unit === unit);
            return (
              <div key={unit} className="rental-quote-option">
                <label className="quote-checkbox">
                  <input
                    type="checkbox"
                    checked={index >= 0}
                    onChange={(e) =>
                      e.target.checked
                        ? onChange([...quotes, { unit, amount: "" }])
                        : remove(index)
                    }
                  />
                  元 / {rentalUnitLabels[unit]}
                </label>
                {index >= 0 && (
                  <input
                    aria-label={`元 / ${rentalUnitLabels[unit]}报价`}
                    inputMode="decimal"
                    maxLength={14}
                    value={quotes[index].amount}
                    placeholder="填写报价"
                    onChange={(e) => update(index, { amount: e.target.value })}
                  />
                )}
                {index >= 0 &&
                  (index === 0 ? (
                    <small>优先展示</small>
                  ) : (
                    <button
                      type="button"
                      className="text-button"
                      onClick={() => prioritize(index)}
                    >
                      优先展示此报价
                    </button>
                  ))}
              </div>
            );
          })}
      </div>
      {allowed.includes("period") && (
        <section className="rental-periods" aria-label="时段报价">
          <p>
            自定义时段报价 · 例如包夜、白天、晚上，最多 12
            条。时间可留空，跨午夜自动标记次日。
          </p>
          {quotes.map(
            (quote, index) =>
              quote.unit === "period" && (
                <div className="rental-period-row" key={index}>
                  <label>
                    时段名称
                    <input
                      aria-label={`时段 ${index + 1} 名称`}
                      maxLength={60}
                      value={quote.label ?? ""}
                      placeholder="例如包夜、白天"
                      onChange={(e) => update(index, { label: e.target.value })}
                    />
                  </label>
                  <label>
                    计价单位
                    <select
                      aria-label={`时段 ${index + 1} 计价单位`}
                      value={quote.billing_unit ?? "period"}
                      onChange={(e) =>
                        update(index, {
                          billing_unit: e.target
                            .value as RentalQuote["billing_unit"],
                        })
                      }
                    >
                      {rentalUnits.map((unit) => (
                        <option key={unit} value={unit}>
                          元 / {rentalUnitLabels[unit]}
                        </option>
                      ))}
                    </select>
                  </label>
                  <label>
                    开始时间
                    <input
                      aria-label={`时段 ${index + 1} 开始时间`}
                      type="time"
                      step="60"
                      value={quote.start_time ?? ""}
                      onChange={(e) =>
                        update(index, { start_time: e.target.value })
                      }
                    />
                  </label>
                  <label>
                    结束时间
                    <input
                      aria-label={`时段 ${index + 1} 结束时间`}
                      type="time"
                      step="60"
                      value={quote.end_time ?? ""}
                      onChange={(e) =>
                        update(index, { end_time: e.target.value })
                      }
                    />
                  </label>
                  <label>
                    报价（元 /{" "}
                    {rentalUnitLabels[quote.billing_unit ?? "period"]}）
                    <input
                      aria-label={`时段 ${index + 1} 报价`}
                      inputMode="decimal"
                      maxLength={14}
                      value={quote.amount}
                      placeholder="填写报价"
                      onChange={(e) =>
                        update(index, { amount: e.target.value })
                      }
                    />
                  </label>
                  <button
                    type="button"
                    className="text-button"
                    aria-label={`移除时段 ${index + 1}`}
                    onClick={() => remove(index)}
                  >
                    移除
                  </button>
                  <small>{periodLabel(quote)}</small>
                  {index === 0 ? (
                    <small>优先展示</small>
                  ) : (
                    <button
                      type="button"
                      className="text-button"
                      onClick={() => prioritize(index)}
                    >
                      优先展示此报价
                    </button>
                  )}
                </div>
              ),
          )}
          <button
            type="button"
            className="button-outline"
            disabled={quotes.filter((q) => q.unit === "period").length >= 12}
            onClick={() =>
              onChange([
                ...quotes,
                {
                  unit: "period",
                  label: "",
                  billing_unit: "period",
                  amount: "",
                  start_time: "",
                  end_time: "",
                },
              ])
            }
          >
            添加时段报价
          </button>
        </section>
      )}
    </div>
  );
}
