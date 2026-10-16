// @vitest-environment jsdom
import { useState } from 'react';
import { QueryClient, QueryClientProvider } from '@tanstack/react-query';
import { cleanup, fireEvent, render, screen, waitFor, within } from '@testing-library/react';
import { afterEach, describe, expect, it, vi } from 'vitest';
import { activityOutputPaths, decisionQuestionErrors } from './model';
import type { FormSchema } from './model';
import { resolvePickerSchema, SchemaFields } from './schema-form';

afterEach(() => { cleanup(); vi.unstubAllGlobals(); });

const questionSchema: FormSchema = {
 type:'object', properties:{questions:{type:'array',title:'Questions','x-decision-questions':true,items:{type:'object',properties:{key:{type:'string',title:'Key'},instructions:{type:'string',title:'Instructions'},type:{type:'string',enum:['choice','noul','score'],default:'choice'},options:{type:'array',title:'Options',items:{type:'object',properties:{key:{type:'string'},text:{type:'string'}},required:['key','text']}}},required:['key','instructions','options']}}},required:['questions'],
};
function Editor(){
 const [value,setValue]=useState<Record<string,unknown>>({questions:[{key:'first',instructions:'First?',options:[{key:'a',text:'A'},{key:'b',text:'B'}]},{key:'second',instructions:'Second?',options:[{key:'c',text:'C'},{key:'d',text:'D'}]}]});
 return <><SchemaFields schema={questionSchema} data={value} onChange={setValue} workspaceId="ws" tokens/><output data-testid="saved">{JSON.stringify(value)}</output></>;
}
describe('decision authoring',()=>{
 it('keeps question and option order while editing and reordering',()=>{
  render(<Editor/>);
  const input=screen.getAllByLabelText('Key *')[0];
  fireEvent.change(input,{target:{value:'changed'}});
  expect(screen.getByTestId('saved').textContent).toContain('changed');
  // First two down controls belong to the first question's two options.
  fireEvent.click(screen.getAllByRole('button',{name:'Move down'})[2]);
  expect(screen.getAllByLabelText('Key *')[0]).toHaveProperty('value','second');
  expect(screen.getByTestId('saved').textContent?.indexOf('second')).toBeLessThan(screen.getByTestId('saved').textContent?.indexOf('changed')??0);
 });
 it('validates typed question semantics and exposes configured output paths',()=>{
  expect(decisionQuestionErrors([{key:'valid',type:'noul',instructions:'Valid?',options:[{key:'yes',text:'Y'},{key:'no',text:'N'}]}]).join(' ')).toContain('false then true');
  expect(decisionQuestionErrors([{key:'priority',type:'score',instructions:'Priority?',options:[{key:'low',text:'Low'},{key:'high',text:'High'}]}])).toEqual([]);
  expect(activityOutputPaths({key:'ai.decide',kind:'action',ports:['done'],description:'',input_schema:{}},{questions:[{key:'priority',type:'score'}]})).toContain('answers.priority.score');
 });
 it('defers option-array validation for tokens while checking literal siblings',()=>{
  expect(decisionQuestionErrors([{key:'route',instructions:'Choose?',options:'{input:options}'}])).toEqual([]);
  expect(decisionQuestionErrors([{key:'valid',type:'noul',instructions:'Valid?',options:'{input:options}'}])).toEqual([]);
  expect(decisionQuestionErrors([{key:'invalid.key',instructions:'',options:'{input:options}'}]).join(' ')).toContain('instructions are required');
  expect(decisionQuestionErrors([{key:'route',instructions:'Choose?',options:[]}]).join(' ')).toContain('2–20 ordered options');
 });
 it('keeps the editor usable when options switch to a token and back',()=>{
  render(<Editor/>);
  const header=screen.getAllByText('Options *')[0].parentElement!;
  const toggle=within(header).getByRole('checkbox',{name:'Token'});
  fireEvent.click(toggle);
  const input=screen.getByLabelText('Options *');
  fireEvent.change(input,{target:{value:'{input:options}'}});
  expect(JSON.parse(screen.getByTestId('saved').textContent!).questions[0].options).toBe('{input:options}');
  fireEvent.click(toggle);
  expect(JSON.parse(screen.getByTestId('saved').textContent!).questions[0].options).toEqual([]);
  expect(screen.getAllByRole('button',{name:'Add options'})).toHaveLength(2);
 });
 it('constrains static term dependencies and leaves dynamic IDs for token input',()=>{
  const schema:FormSchema={type:'string','x-papergo':{kind:'terms'},'x-term-set-sibling':'term_set_id'};
  expect(resolvePickerSchema(schema,{term_set_id:'set-1'})['x-papergo']?.term_set_id).toBe('set-1');
  const dynamic=resolvePickerSchema(schema,{term_set_id:'{input:set}'});
  expect(dynamic['x-papergo']?.term_set_id).toBeUndefined();
  expect(dynamic.description).toContain('tokens');
 });
});

it('uses the terms picker to select a set ID with its group filter', async () => {
 const fetch = vi.fn(async () => new Response(JSON.stringify({value:[{id:'set-1',name:'Document topics'}]})));
 vi.stubGlobal('fetch',fetch);
 const client = new QueryClient({defaultOptions:{queries:{retry:false}}});
 try {
  const schema:FormSchema={type:'object',properties:{term_set_id:{type:'string',title:'Term set','x-papergo':{kind:'terms',select:'sets',group_id:'group-1'}}}};
  render(<QueryClientProvider client={client}><SchemaFields schema={schema} data={{term_set_id:'set-1'}} onChange={vi.fn()} workspaceId="ws"/></QueryClientProvider>);
  await waitFor(()=>expect(screen.getByRole('combobox',{name:'Term set'}).textContent).toContain('Document topics'));
  expect(fetch.mock.calls).toHaveLength(1);
  const url = new URL(String((fetch.mock.calls as unknown[][])[0][0]));
  expect(url.pathname).toBe('/v1/term-sets');
  expect(url.searchParams.get('group_id')).toBe('group-1');
 } finally { client.clear(); }
});
