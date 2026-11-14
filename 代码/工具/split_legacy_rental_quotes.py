#!/usr/bin/env python3
"""Preview and audit conservative title-price extraction for untouched R5 imports.

Only the two named local test databases are accepted. Preview is the default;
apply recomputes the plan, checks every source row under a write lock, and keeps
a database backup. Original title, legacy_payload, legacy_price and category stay intact.
"""
from __future__ import annotations

import argparse
from collections import Counter
from decimal import Decimal
import hashlib
import json
import os
from pathlib import Path
import re
import subprocess
import unicodedata

RULE = 'r212-title-quotes-v1'
ORDER = {'hour': 0, 'day': 1, 'week': 2, 'month': 3, 'period': 4}
UNITS = {'时': 'hour', '小时': 'hour', 'h': 'hour', '日': 'day', '天': 'day',
         '整日': 'day', '周': 'week', '月': 'month', '包夜': 'period', '夜': 'period'}
NUMBER = r'[0-9]{1,10}(?:\.[0-9]{1,2})?'
UNIT = r'(?:包夜|整日|小时|时|日|天|周|月|h|夜)'
TOKEN = re.compile(
    rf'(?P<u1>{UNIT})(?:租)?\s*[:/：]?\s*(?P<n1>{NUMBER})(?![0-9.])'
    rf'|(?<![0-9.\-])(?P<n2>{NUMBER})\s*(?:元|块钱|块|r)?\s*(?:/|每|一[个]?)?\s*(?P<u2>{UNIT})', re.I)
REGION = re.compile(r'一舞|拾忆|拾亿|十年|华[东南北中]|东[北南]|西[南北]|QQ炫舞')
COMPLEX = re.compile(r'早|晚|白天|白时|长白|上午|下午|中午|凌晨|时段|时间段|点|工作日|周末|节假日|打包|包价|季|年|双周|两个月|\d+[-~]\d+')


def money(raw):
    value = Decimal(raw)
    if value <= 0 or value >= Decimal('10000000000'):
        raise ValueError('Price must be positive and fit numeric(12,2)')
    return f'{value:.2f}'


def rental_header(title):
    text = unicodedata.normalize('NFKC', title).strip()
    text = re.sub(r'^QYZH\d+\s*[:：]?\s*', '', text, flags=re.I)
    region = REGION.search(text)
    head = text[:region.start()] if region else text[:180]
    out = head.find('出租')
    if out >= 0 and not re.match(r'\s*[（(].{0,60}(?:点|早|晚)', head[out + 2:]):
        head = head[:out]
    return head


def parse_title(title):
    """Parse unqualified amount/unit sequences; scoped prices need explicit review.

    The intentional small grammar avoids turning 3小时起租 into a 3-yuan rate,
    reading 白天8时 as 8/day, or silently losing the scope of a weekly period.
    """
    head = rental_header(title)
    if COMPLEX.search(head):
        return {'quotes': [], 'notes': ['review_scoped_prices'], 'header': head}
    masked = re.sub(r'(?:[0-9]+|[一二三四五六七八九十]+)(?:个)?(?:小时|h)\s*起(?:租|步)?', '', head, flags=re.I)
    candidates, notes, spans = {}, [], []
    for match in TOKEN.finditer(masked):
        spans.append(match.span())
        raw_unit = (match['u1'] or match['u2']).lower()
        try:
            amount = money(match['n1'] or match['n2'])
        except (ValueError, ArithmeticError):
            notes.append('invalid_amount')
            continue
        unit = UNITS[raw_unit]
        quote = {'unit': unit, 'amount': amount}
        if unit == 'period':
            quote.update(label='包夜', billing_unit='period')
        candidates.setdefault(unit, []).append(quote)
    remainder = list(masked)
    for start, end in spans:
        remainder[start:end] = ' ' * (end-start)
    if re.search(r'[0-9]', ''.join(remainder)):
        notes.append('unassigned_number')
    quotes = []
    for unit, values in candidates.items():
        if len({q['amount'] for q in values}) != 1:
            notes.append('conflicting_' + unit)
        else:
            quotes.append(values[0])
    if notes:
        # Do not partially publish a pricing header whose grammar was ambiguous.
        quotes = []
    return {'quotes': sorted(quotes, key=lambda q: ORDER[q['unit']]),
            'notes': sorted(set(notes)), 'header': head}


def validate_quotes(quotes):
    if not isinstance(quotes, list) or len(quotes) > 16:
        raise ValueError('Invalid reviewed quote list')
    keys, periods = set(), 0
    for quote in quotes:
        if not isinstance(quote, dict) or quote.get('unit') not in ORDER:
            raise ValueError('Invalid reviewed quote unit')
        if not isinstance(quote.get('amount'), str) or not re.fullmatch(NUMBER, quote['amount']):
            raise ValueError('Invalid reviewed amount')
        quote['amount'] = money(quote['amount'])
        if quote['unit'] == 'period':
            periods += 1
            if set(quote) - {'unit','amount','label','billing_unit','start_time','end_time'}:
                raise ValueError('Unknown period field')
            if not isinstance(quote.get('label'), str) or not 1 <= len(quote['label'].strip()) <= 30:
                raise ValueError('Invalid period name')
            quote['label'] = quote['label'].strip()
            if any(unicodedata.category(char) == 'Cc' for char in quote['label']):
                raise ValueError('Invalid period name')
            if quote.get('billing_unit') not in ORDER:
                raise ValueError('Invalid period billing basis')
            start, end = quote.get('start_time'), quote.get('end_time')
            if start or end:
                if not all(isinstance(t,str) and re.fullmatch(r'(?:[01]\d|2[0-3]):[0-5]\d',t) for t in (start,end)) or start == end:
                    raise ValueError('Invalid period clock')
            key = ('period', quote['label'], quote.get('billing_unit'), start, end)
        else:
            if set(quote) != {'unit','amount'}:
                raise ValueError('Normal quote must not contain period metadata')
            key = (quote['unit'],)
        if key in keys:
            raise ValueError('Duplicate reviewed quote')
        keys.add(key)
    if periods > 12:
        raise ValueError('Too many period quotes')
    return quotes


def canonical(value):
    return json.dumps(value, ensure_ascii=False, sort_keys=True, separators=(',', ':'))


def digest(value):
    return hashlib.sha256(canonical(value).encode()).hexdigest()


def source_fields():
    return "id,product_code,title,trade_type,price_amount::text,price_unit,legacy_price,legacy_category,rental_quotes,category_id,status,version,approved_version,source,updated_at::text"


def make_plan(rows, database, reviewed=None, expected_version=2):
    reviewed = reviewed or {}
    entries, skipped = [], []
    for row in rows:
        expected_unit = {'时日租号': 'day', '周月租号': 'month'}.get(row.get('legacy_category'))
        untouched = (row.get('source') == 'legacy' and row.get('version') == expected_version and not row.get('rental_quotes')
                     and row.get('price_unit') == expected_unit and expected_unit
                     and row.get('category_id') == {'day': 'legacy-short-rent', 'month': 'legacy-long-rent'}[expected_unit])
        try:
            untouched = untouched and Decimal(row['price_amount']) == Decimal(row['legacy_price'])
        except (ValueError, TypeError, ArithmeticError):
            untouched = False
        if not untouched:
            skipped.append({'id': row['id'], 'reason': 'not_untouched_r5_import'})
            continue
        parsed = parse_title(row['title'])
        if row['id'] in reviewed:
            review = reviewed[row['id']]
            if review['title_sha256'] != hashlib.sha256(row['title'].encode()).hexdigest():
                raise ValueError('Reviewed title has changed')
            parsed['quotes'] = validate_quotes(review['quotes'])
            parsed['notes'] = review.get('notes', []) + ['individually_reviewed']
        quotes = parsed['quotes']
        # No reliable price is safer than displaying an old amount in a guessed unit.
        new = {'rental_quotes': quotes, 'price_unit': quotes[0]['unit'] if quotes else 'unknown',
               'price_amount': quotes[0]['amount'] if quotes else None}
        entries.append({'before': row, 'after': new, 'notes': parsed['notes'], 'header': parsed['header']})
    return {'rule': RULE, 'database': database, 'expected_version': expected_version,
            'reviewed_sha256': digest(reviewed), 'entries': entries, 'skipped': skipped}


def summary(plan):
    return {'rule': RULE, 'database': plan['database'], 'updated_candidates': len(plan['entries']),
            'with_quotes': sum(bool(e['after']['rental_quotes']) for e in plan['entries']),
            'with_periods': sum(any(q['unit'] == 'period' for q in e['after']['rental_quotes']) for e in plan['entries']),
            'without_clear_price': sum(not e['after']['rental_quotes'] for e in plan['entries']),
            'skipped': len(plan['skipped']), 'notes': dict(Counter(n for e in plan['entries'] for n in e['notes'])),
            'plan_sha256': digest(plan)}


def sql_string(value):
    return "'" + value.replace("'", "''") + "'"


def apply_statement(plan):
    expected = [{'before': e['before'], 'after': e['after'], 'notes': e['notes']} for e in plan['entries']]
    # Comparison uses the same JSON projection as preview. The lock plus exact
    # snapshot rejects any intervening edit and prevents mixed-version publication.
    return """BEGIN;
SET LOCAL lock_timeout='10s';
SET LOCAL statement_timeout='60s';
LOCK TABLE products IN SHARE ROW EXCLUSIVE MODE;
LOCK TABLE categories IN SHARE MODE;
CREATE TEMP TABLE r212_plan ON COMMIT DROP AS SELECT value AS item FROM jsonb_array_elements(%s::jsonb);
DO $r212$
BEGIN
 IF EXISTS(SELECT 1 FROM r212_plan e LEFT JOIN (SELECT %s FROM products) p ON p.id=e.item->'before'->>'id'
           WHERE p.id IS NULL OR to_jsonb(p) IS DISTINCT FROM e.item->'before') THEN
   RAISE EXCEPTION 'Rental source changed; create and review a new plan';
 END IF;
END $r212$;
WITH changed AS (
 UPDATE products p SET rental_quotes=e.item->'after'->'rental_quotes',
 price_unit=e.item->'after'->>'price_unit', price_amount=(e.item->'after'->>'price_amount')::numeric,
 approved_version=CASE WHEN p.approved_version=p.version THEN p.version+1 ELSE p.approved_version END,
 version=p.version+1,updated_at=now()
 FROM r212_plan e WHERE p.id=e.item->'before'->>'id' RETURNING p.id
), logged AS (
INSERT INTO admin_logs(id,action,resource_id,detail)
 SELECT 'r212:title:'||c.id, 'maintenance.rental_title_quotes',c.id,
 jsonb_build_object('rule',%s,'before',(e.item->'before')-'title','after',e.item->'after',
 'notes',e.item->'notes','plan_sha256',%s,'reviewed_sha256',%s)
 FROM changed c JOIN r212_plan e ON c.id=e.item->'before'->>'id' RETURNING resource_id
)
SELECT count(*) FROM logged;
COMMIT;
""" % (sql_string(canonical(expected)), source_fields(), sql_string(RULE), sql_string(digest(plan)), sql_string(plan['reviewed_sha256']))


def main():
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument('--database', required=True, choices=['qingye_acceptance', 'qingye_test'])
    parser.add_argument('--plan', required=True, type=Path)
    parser.add_argument('--apply', action='store_true')
    parser.add_argument('--reviewed', type=Path, help='Private, individually reviewed title-bound quote overrides')
    parser.add_argument('--backup', type=Path, help='New private dump path; required for apply')
    args = parser.parse_args()
    os.umask(0o077)

    def sql(statement):
        return subprocess.check_output(['runuser', '-u', 'postgres', '--', 'psql', '-X', '-qAt',
                                        '-v', 'ON_ERROR_STOP=1', '-d', args.database], input=statement, text=True, cwd='/tmp')
    rows = json.loads(sql("SELECT coalesce(json_agg(t),'[]') FROM (SELECT " + source_fields() + " FROM products WHERE trade_type='rent' AND deleted_at IS NULL ORDER BY id) t"))
    migrated = sql("SELECT count(*) FROM schema_migrations WHERE version LIKE '0017_%'").strip() == '1'
    reviewed = json.loads(args.reviewed.read_text(encoding='utf-8')) if args.reviewed else {}
    fresh = make_plan(rows, args.database, reviewed, 3 if migrated else 2)
    if args.apply:
        plan = json.loads(args.plan.read_text())
        if digest(plan) != digest(fresh):
            raise RuntimeError('Plan differs from current source; preview again')
        if not args.backup or args.backup.exists():
            raise ValueError('Apply requires a new backup path')
        with args.backup.open('xb') as stream:
            subprocess.run(['runuser', '-u', 'postgres', '--', 'pg_dump', '-Fc', args.database], cwd='/tmp', stdout=stream, check=True)
        subprocess.run(['pg_restore', '--list', str(args.backup)], check=True, stdout=subprocess.DEVNULL)
        result = sql(apply_statement(fresh)).strip()
        if result != str(len(fresh['entries'])):
            raise RuntimeError('Unexpected update count')
        report = summary(fresh) | {'applied': True, 'backup_sha256': hashlib.sha256(args.backup.read_bytes()).hexdigest()}
        args.plan.with_suffix('.applied.json').write_text(json.dumps(report, ensure_ascii=False, indent=2) + '\n')
        print(json.dumps(report, ensure_ascii=False))
    else:
        with args.plan.open('x', encoding='utf-8') as stream:
            json.dump(fresh, stream, ensure_ascii=False, indent=2)
            stream.write('\n')
        print(json.dumps(summary(fresh), ensure_ascii=False))


if __name__ == '__main__':
    main()
