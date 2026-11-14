import copy
import hashlib
import unittest

from split_legacy_rental_quotes import apply_statement, make_plan, parse_title, validate_quotes


class RentalTitleTests(unittest.TestCase):
    def test_compact_amount_first(self):
        self.assertEqual(parse_title('QYZH123: 4时20日120周一舞彩钻女')['quotes'], [
            {'unit':'hour','amount':'4.00'}, {'unit':'day','amount':'20.00'}, {'unit':'week','amount':'120.00'}])

    def test_unit_first_and_original_price_not_used(self):
        self.assertEqual(parse_title('QYZH321：日20周60月220出租账号')['quotes'], [
            {'unit':'day','amount':'20.00'}, {'unit':'week','amount':'60.00'}, {'unit':'month','amount':'220.00'}])

    def test_minimum_hours_not_a_quote(self):
        result = parse_title('QYZH100：10/小时 3小时起租 包夜30 450周1660月出租一舞')
        self.assertEqual(len(result['quotes']),4)
        self.assertEqual(result['quotes'][0], {'unit':'hour','amount':'10.00'})
        self.assertEqual(result['quotes'][-1], {'unit':'period','label':'包夜','billing_unit':'period','amount':'30.00'})

    def test_scoped_rates_cannot_be_misread_as_ordinary(self):
        for text in ['白天8时,晚上10时,80日', '(早7到晚7)周140月500', '时租10点-22点月650,整租50日330周1200月',
                     '110周450月(晚上12-中午12)', '工作日100/天,周末120/天', '18-24点13一个小时',
                     '20日90周350月1000季度']:
            with self.subTest(text=text):
                self.assertEqual(parse_title(text)['quotes'], [])

    def test_ambiguous_leftover_or_conflicts_require_review(self):
        for text in ['本月特价1700周460', '日80日100', '新增仙露5外搭,10时60日328周1000月']:
            with self.subTest(text=text):
                self.assertEqual(parse_title(text)['quotes'], [])

    def test_no_statistics_deposits_or_arbitrary_decimal_tail(self):
        for text in ['一舞男外搭120周月可租', '出租一舞,结婚押金200,主题18.4W,对戒20页', 'QYZH987：价格面议一舞金钻']:
            self.assertEqual(parse_title(text)['quotes'], [])

    def fixture(self):
        return dict(id='fixture',product_code='QYZH1',title='4时20日120周出租一舞',price_amount='120.00',
                    price_unit='day',legacy_price='120',legacy_category='时日租号',rental_quotes=[],
                    category_id='legacy-short-rent',status='published',version=2,approved_version=2,
                    source='legacy',updated_at='2026-01-01 00:00:00+00',trade_type='rent')

    def test_only_untouched_import_is_eligible(self):
        for field,value in [('source','platform'),('version',4),('price_unit','hour'),('price_amount','100.00'),
                            ('category_id','custom'),('rental_quotes',[{'unit':'day','amount':'20'}])]:
            row=self.fixture(); row[field]=value
            with self.subTest(field=field):
                self.assertEqual(make_plan([row], 'qingye_acceptance')['entries'], [])

    def test_new_category_migration_version_guard(self):
        row=self.fixture(); row['version']=3; row['approved_version']=3
        self.assertEqual(len(make_plan([row], 'qingye_acceptance', expected_version=3)['entries']),1)
        self.assertEqual(make_plan([row], 'qingye_acceptance')['entries'],[])

    def test_reviewed_quotes_bound_to_exact_title(self):
        row=self.fixture(); row['title']='白天7时 晚上10时'
        reviewed={'fixture':{'title_sha256':hashlib.sha256(row['title'].encode()).hexdigest(),
                             'quotes':[{'unit':'period','label':'白天','billing_unit':'hour','amount':'7'}]}}
        plan=make_plan([row], 'qingye_acceptance', reviewed)
        self.assertEqual(plan['entries'][0]['after']['rental_quotes'][0]['amount'],'7.00')
        row['title']+='改价'
        with self.assertRaisesRegex(ValueError,'title has changed'):
            make_plan([row], 'qingye_acceptance', reviewed)

    def test_period_validation_and_no_unit_conversion(self):
        base={'unit':'period','label':'包夜','billing_unit':'period','amount':'25', 'start_time':'23:00','end_time':'08:00'}
        self.assertEqual(validate_quotes([copy.deepcopy(base)])[0]['amount'],'25.00')
        for patch in [dict(start_time='24:00'),dict(end_time=None),dict(end_time='23:00'),dict(label=''),dict(billing_unit='year')]:
            with self.assertRaises(ValueError): validate_quotes([base|patch])
        with self.assertRaises(ValueError): validate_quotes([copy.deepcopy(base),copy.deepcopy(base)])
        with self.assertRaises(ValueError): validate_quotes([copy.deepcopy(base),base|{'label':' 包夜 '}])
        with self.assertRaises(ValueError): validate_quotes([base|{'label':'包\u0001夜'}])
        with self.assertRaises(ValueError): validate_quotes([{'unit':'hour','amount':'5','label':'白天'}])

    def test_plan_has_snapshot_lock_and_no_legacy_overwrite(self):
        plan=make_plan([self.fixture()], 'qingye_acceptance')
        statement=apply_statement(plan)
        self.assertIn('LOCK TABLE products IN SHARE ROW EXCLUSIVE MODE',statement)
        self.assertIn('to_jsonb(p) IS DISTINCT FROM',statement)
        self.assertIn('RAISE EXCEPTION',statement)
        self.assertIn("'maintenance.rental_title_quotes'",statement)
        self.assertIn("'notes',e.item->'notes'",statement)
        self.assertIn("'reviewed_sha256'",statement)
        self.assertIn('SELECT count(*) FROM logged',statement)
        for field in ['legacy_price=', 'legacy_category=', 'legacy_payload=', 'title=', 'status=', 'published_at=']:
            self.assertNotIn(field,statement)

    def test_repeat_apply_skips_updated_import(self):
        row=self.fixture(); plan=make_plan([row], 'qingye_acceptance')
        row.update(plan['entries'][0]['after']); row['version']+=1
        self.assertEqual(make_plan([row], 'qingye_acceptance')['entries'],[])


if __name__ == '__main__':
    unittest.main()
