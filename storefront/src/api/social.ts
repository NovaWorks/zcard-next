import { api } from './client';
export interface SocialField {
 key:string;label:string;type:'text'|'url'|'username'|'integer'|'lines'|'date'|'enum';required:boolean;
 min?:number;max?:number;max_length?:number;min_items?:number;max_items?:number;options?:string[];placeholder?:string;
}
export interface SocialSchema {
 version:number;revision:string;profile:string;fields:SocialField[];
 quantity:{mode:'explicit'|'derived_lines'|'absent'|'range_per_event';field?:string;lines_field?:string;line_policy?:'all'|'nonempty';min?:number;max?:number;step?:number;step_base?:number;min_field?:string;max_field?:string};
 billing:{mode:string;currency:string;verified:boolean};cancel:boolean;refill:boolean;purchasable:boolean;unavailable_reason?:string;
}
export interface SocialQuote {quote_id:string;amount_cents:number;currency:string;expires_at:number;quantity?:number}
export interface SocialOrder {
 state:string;phase:string;snapshot?:{version?:string|number;quantity?:number;start_count?:number;remains?:number;completed_quantity?:number;settlement_state?:string};
 paid_amount_cents:number;refunded_amount_cents:number;refund_status:string;can_cancel:boolean;can_refill:boolean;active:boolean;message?:string;
 operation_status?:string;operation_action?:string;operation_request_id?:string;
}
const productPath=(id:number|string)=>`/products/${encodeURIComponent(String(id))}/social`;
export const socialSchema=(id:number|string)=>api.get<SocialSchema>(productPath(id)+'/schema');
export const socialQuote=(id:number|string,schema_revision:string,parameters:Record<string,string>)=>api.post<SocialQuote>(productPath(id)+'/quotes',{schema_revision,parameters});
export const buySocial=(id:number|string,quote_id:string,request_id:string)=>api.post<{order_no:string;total_cents:number}>(productPath(id)+'/orders',{quote_id,request_id});
export const getSocial=(no:string)=>api.get<SocialOrder>(`/orders/${encodeURIComponent(no)}/social`);
export const actSocial=(no:string,action:'cancel'|'refill',request_id:string)=>api.post<SocialOrder>(`/orders/${encodeURIComponent(no)}/social/${action}`,{request_id});
export function socialLineCount(value:string,policy:'all'|'nonempty'='nonempty'):number{return policy==='all'?value.split('\n').length:value.split('\n').filter(line=>line.trim()!=='').length}
export function socialSchemaSupported(schema:SocialSchema):boolean{
 const types=['text','url','username','integer','lines','date','enum'];
 return schema?.version===1 && Array.isArray(schema.fields) && schema.fields.length>0 && new Set(schema.fields.map(f=>f.key)).size===schema.fields.length && schema.fields.every(f=>!!f.key && types.includes(f.type)) && ['explicit','derived_lines','absent','range_per_event'].includes(schema.quantity?.mode);
}
