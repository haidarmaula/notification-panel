"""Static documentation artifact and deployment safety regressions."""
import hashlib
import importlib.util
import io
import json
from pathlib import Path
import tarfile
import tempfile
import unittest
from unittest.mock import patch
from ops_docs import validate_bundle

ROOT=Path(__file__).resolve().parents[2]
spec=importlib.util.spec_from_file_location('api_docs',ROOT/'scripts/api_docs.py');builder=importlib.util.module_from_spec(spec);spec.loader.exec_module(builder)

class DocsTests(unittest.TestCase):
    def test_gateway_recovers_original_stopped_before_rename(self):
        import ops_gateway_docs as gateway
        with tempfile.TemporaryDirectory() as directory:
            root=Path(directory)
            (root/'docs-gateway-journal.json').write_text('{"phase":"prepared"}')
            (root/'gateway-before-docs.conf').write_bytes(b'original')
            commands=[]
            with patch.multiple(gateway,ROOT=root,write_config=lambda _:None,alive=lambda _:False,
                inspect=lambda _:{'State':{'Running':False}},run=lambda args:commands.append(args),api_ready=lambda:None):
                gateway.recover()
            self.assertEqual(commands,[['docker','start','hello-staging-gateway']])
            self.assertFalse((root/'docs-gateway-journal.json').exists())

    def test_capture_preserves_documentation_release(self):
        import ops_release as release
        from types import SimpleNamespace
        verified=[]
        with tempfile.TemporaryDirectory() as directory:
            docs=SimpleNamespace(current=lambda:'prior-reference',verify=lambda value:verified.append(value))
            with patch.multiple(release,ROOT=Path(directory),state=lambda:{'schema':{'version':41,'dirty':False}},
                inspect=lambda _:{'Image':'sha256:'+'a'*64},docs=docs):
                manifest=release.capture('captured-reference')
            self.assertEqual(manifest['docs_release'],'prior-reference')
            self.assertEqual(verified,['prior-reference'])

    def bundle(self,release='test-reference'):
        with tempfile.TemporaryDirectory() as d:
            manifest=builder.build(Path(d)/'docs',release)
            out=io.BytesIO()
            with tarfile.open(fileobj=out,mode='w') as tar:
                for p in sorted((Path(d)/'docs').rglob('*')):
                    if p.is_file():tar.add(p,arcname=str(p.relative_to(Path(d)/'docs')),recursive=False)
            return out.getvalue(),manifest
    def test_preserves_canonical_contracts_and_pins_asset_urls(self):
        data,manifest=self.bundle()
        validate_bundle(data,hashlib.sha256(data).hexdigest(),'test-reference')
        with tarfile.open(fileobj=io.BytesIO(data)) as t:
            for _,_,file,_ in builder.GUIDES:self.assertEqual(t.extractfile('contracts/'+file).read(),(ROOT/'docs/contracts'/file).read_bytes())
            html=t.extractfile('index.html').read().decode()
            self.assertIn('/docs/releases/test-reference/app.js',html)
            self.assertNotIn('__DOCS_BASE__',html)
            self.assertNotIn('https://',html)
            catalog=json.load(t.extractfile('catalog.json'));self.assertEqual(sum(g['operations'] for g in catalog['guides']),57)
    def test_tampered_or_wrong_release_artifact_is_rejected(self):
        data,_=self.bundle()
        with self.assertRaises(ValueError):validate_bundle(data,'0'*64,'test-reference')
        with self.assertRaises(ValueError):validate_bundle(data,hashlib.sha256(data).hexdigest(),'wrong-reference')
    def test_symlink_or_traversal_is_never_extracted(self):
        for name,kind in [('../gateway.key',tarfile.REGTYPE),('escape',tarfile.SYMTYPE)]:
            out=io.BytesIO()
            with tarfile.open(fileobj=out,mode='w') as tar:
                entry=tarfile.TarInfo(name);entry.type=kind;entry.linkname='/gateway/gateway.key';tar.addfile(entry)
            data=out.getvalue()
            with self.assertRaises(ValueError):validate_bundle(data,hashlib.sha256(data).hexdigest(),'test-reference')

if __name__=='__main__':unittest.main()
