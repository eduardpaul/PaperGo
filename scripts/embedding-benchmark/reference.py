#!/usr/bin/env python3
"""Regenerate integration vectors with independent Python HF Tokenizers and ORT.
Install onnxruntime==1.23.2 and tokenizers==0.23.3 in a development venv.
"""
import argparse
import json
from pathlib import Path
import numpy as np
import onnxruntime as ort
from tokenizers import Tokenizer

parser=argparse.ArgumentParser(description=__doc__)
parser.add_argument('--models-root',type=Path,default=Path('.tools/embedding/models'))
parser.add_argument('--output',type=Path,default=Path('internal/embedding/testdata/reference.json'))
args=parser.parse_args()
query='¿Dónde está la factura pagada de electricidad?'
document={'Title':'Electricity bill','Text':'Electricity invoice for September was paid in full by bank transfer on 3 October.'}
result=[]
for model in ['embeddinggemma-2','harrier-oss-v1-0.6b']:
 path=args.models_root/model
 tokenizer=Tokenizer.from_file(str(path/'tokenizer.json'))
 options=ort.SessionOptions();options.intra_op_num_threads=1;options.inter_op_num_threads=1
 options.enable_cpu_mem_arena=False;options.enable_mem_pattern=False
 session=ort.InferenceSession(str(path/'onnx/model_q4.onnx'),options,providers=['CPUExecutionProvider'])
 prompts=['task: search result | query: '+query,'title: '+document['Title']+' | text: '+document['Text']]
 if model.startswith('harrier'):
  prompts=['Instruct: Given a web search query, retrieve relevant passages that answer the query\nQuery: '+query,document['Title']+'\n'+document['Text']]
 vectors=[];ids=[]
 for text in prompts:
  encoded=tokenizer.encode(text,add_special_tokens=True);ids.append(encoded.ids)
  inputs={'input_ids':np.array([encoded.ids],dtype=np.int64),'attention_mask':np.ones((1,len(encoded.ids)),dtype=np.int64)}
  if model=='embeddinggemma-2':inputs.update({name:np.empty((0,512),dtype=np.float32) for name in ['image_features','video_features','audio_features']})
  vector=session.run(['sentence_embedding'],inputs)[0][0].astype(np.float64)
  vectors.append((vector/np.linalg.norm(vector)).tolist())
 result.append({'model':model,'query':query,'document':document,'prompts':prompts,'ids':ids,'vectors':vectors})
args.output.write_text(json.dumps(result,ensure_ascii=False,indent=2)+'\n')
print(args.output)
