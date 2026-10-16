#!/usr/bin/env python3
"""Produce independent official-Python references for workflow serialization and typed questions."""
import argparse
import hashlib
import json
import os
from pathlib import Path

ROOT = Path(__file__).resolve().parents[2]

def canonical(value):
    return json.dumps(value, ensure_ascii=False, sort_keys=True, separators=(',', ':'))

def main():
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument('--directory', type=Path, default=Path('.tools/decision-build'))
    parser.add_argument('--output', type=Path, default=ROOT / 'internal/decision/testdata/typed-references.json')
    args = parser.parse_args()
    manifest = json.loads((ROOT / 'scripts/decision-model/artifacts.json').read_bytes())
    for name, expected in manifest['files'].items():
        with (args.directory / name).open('rb') as source:
            if hashlib.file_digest(source, 'sha256').hexdigest() != expected['sha256']:
                raise ValueError(f'Checksum mismatch: {name}')
    os.environ.update(HF_HUB_OFFLINE='1', TRANSFORMERS_OFFLINE='1', USE_TF='0', LAYA_CPU_AMP='', TOKENIZERS_PARALLELISM='false')
    import torch
    from laya.agent import Agent
    from laya.common import collate_items, render_options
    torch.set_num_threads(1)
    torch.set_num_interop_threads(1)
    agent = Agent(str(args.directory / 'runtime'), device='cpu', compile=False, fast=False)
    definitions = {
        'ordinal': {'type':'score','instructions':'How urgent is this request?', 'criteria':['Routine','Soon','Immediate']},
        'valid': {'type':'noul','instructions':'Does this request concern a payment?', 'criteria':{'false':'No payment issue','true':'Payment issue'}},
        'route': {'type':'choice','instructions':'Which team should handle this request?', 'criteria':['Billing','Technical']},
        'multi': {'type':'choice','instructions':'Which language is used?', 'criteria':['English','Spanish','Catalan','Other']},
    }
    states = [
        ('structured-json', canonical({'subject':'Factura duplicada', 'amount':9007199254740993,'enabled':False,'comments':['Cobro duplicado','Revisió urgent'], 'literal':'{input:secret} <&>'})),
        ('document-context', canonical({'document_text':'Factura de la llum. Total: 57 euros.\n\nPagar abans de divendres.', 'context':{'customer_tier':'premium','previous_contacts':2}})),
    ]
    groups=[]
    for name,state in states:
        for key,q in definitions.items(): agent._check_question(key,q)
        internal={key:agent._to_internal(q) for key,q in definitions.items()}
        items=agent._encode_state(state,list(internal),internal,max_len=1024,head_max_len=256)
        assert all(not item['state_stats']['truncated'] and item['options']['tokens_per_option'] is None for item in items)
        batch=collate_items([items],agent.tok.pad_token_id)
        assert not batch['marker_mask'].all(), 'mixed counts must exercise false mask entries'
        with torch.inference_mode():
            logits,act_logits=agent._infer(batch)
            probabilities=torch.softmax(logits.float(),dim=-1).tolist()
        groups.append({'name':name,'rows':[
            {'request':{'state':state,'question':q['ins'],'options':render_options(q),'type':q['t']},
             'ids':item['ids'],'markers':item['markers'],'qtype':item['qtype'],
             'logits':z[:len(item['markers'])],'act_logits':a,'expected':p[:len(item['markers'])]}
            for q,item,z,a,p in zip(internal.values(),items,logits.tolist(),act_logits.tolist(),probabilities)
        ]})
    args.output.parent.mkdir(parents=True,exist_ok=True)
    args.output.write_text(json.dumps({'model_revision':manifest['revision'],'serializer':'canonical-json-v1 / document-context-json-v1','rendering':'typed-options-v1','groups':groups},ensure_ascii=False,indent=2)+'\n')
    print(args.output)

if __name__=='__main__':
    main()
