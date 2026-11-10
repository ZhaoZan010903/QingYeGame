from pathlib import Path
import base64,re

root=Path(__file__).resolve().parent
source=root/'src'
output=root/'index.html'
text=(source/'index.template.html').read_text(encoding='utf-8')
for token,name in [('__FONT_INTER__','Inter.woff2'),('__FONT_MONO__','JetBrains-Mono.woff2'),('__ASSET_IMAGE__','assets.png')]:
    text=text.replace(token,base64.b64encode((source/name).read_bytes()).decode())
css=(source/'style.css').read_text(encoding='utf-8').replace('#ffffff022','#ffffff02')+'\n'+(source/'refinements.css').read_text(encoding='utf-8')
text=text.replace('__STYLE__',css).replace('__V3_STYLE__',(source/'enhancements.css').read_text(encoding='utf-8'))
script=(source/'app.js').read_text(encoding='utf-8').replace('__V3_SCRIPT__',(source/'enhancements.js').read_text(encoding='utf-8'))
text=text.replace('__SCRIPT__',script)
assert not re.search(r'__(?:FONT|ASSET|STYLE|SCRIPT|V3)[A-Z_]*__',text)
assert 'market-snapshot' not in text.split('<body>',1)[1].split('<script>',1)[0]
output.write_text(text,encoding='utf-8')
(source/'compiled.js').write_text(script,encoding='utf-8')
print(f'Built {output.name}: {output.stat().st_size:,} bytes')
