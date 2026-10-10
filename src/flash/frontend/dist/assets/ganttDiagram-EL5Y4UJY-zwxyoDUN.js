import{g as Le,s as Ae,p as Fe,o as Oe,a as We,b as Ne,_ as l,c as ht,d as Pe,aR as U,l as lt,j as Ve,n as Re,q as ze,y as He}from"./mermaid.core-CAOE6zqC.js";import{c as Mt,g as Et}from"./_commonjsHelpers-BosuxZz1.js";import{R as le,r as Be,q as ue,u as de,C as fe,v as Yt,x as Ge,s as bt}from"./transform-BouDkPPo.js";import{t as je,a as Xe,b as Jt,c as te,d as qe,e as Ue,f as Ze,g as Qe,h as Ke,i as Je,j as ts,k as ee,l as se,m as ie,s as ne,n as re,o as es}from"./time-Cj2cDDRE.js";import{l as ss}from"./linear-D3IXrjaZ.js";import"./app-DgrBOy8j.js";import"./init-Gi6I4Gst.js";import"./defaultLocale-Cw5pbRzx.js";import"./index-C0UdrJPs.js";function is(t,e){let i;if(e===void 0)for(const s of t)s!=null&&(i<s||i===void 0&&s>=s)&&(i=s);else{let s=-1;for(let n of t)(n=e(n,++s,t))!=null&&(i<n||i===void 0&&n>=n)&&(i=n)}return i}function ns(t,e){let i;if(e===void 0)for(const s of t)s!=null&&(i>s||i===void 0&&s>=s)&&(i=s);else{let s=-1;for(let n of t)(n=e(n,++s,t))!=null&&(i>n||i===void 0&&n>=n)&&(i=n)}return i}const rs=Math.PI/180,as=180/Math.PI,Dt=18,he=.96422,me=1,ke=.82521,ye=4/29,mt=6/29,ge=3*mt*mt,os=mt*mt*mt;function pe(t){if(t instanceof st)return new st(t.l,t.a,t.b,t.opacity);if(t instanceof nt)return ve(t);t instanceof le||(t=Be(t));var e=Ft(t.r),i=Ft(t.g),s=Ft(t.b),n=$t((.2225045*e+.7168786*i+.0606169*s)/me),y,g;return e===i&&i===s?y=g=n:(y=$t((.4360747*e+.3850649*i+.1430804*s)/he),g=$t((.0139322*e+.0971045*i+.7141733*s)/ke)),new st(116*n-16,500*(y-n),200*(n-g),t.opacity)}function cs(t,e,i,s){return arguments.length===1?pe(t):new st(t,e,i,s??1)}function st(t,e,i,s){this.l=+t,this.a=+e,this.b=+i,this.opacity=+s}ue(st,cs,de(fe,{brighter(t){return new st(this.l+Dt*(t??1),this.a,this.b,this.opacity)},darker(t){return new st(this.l-Dt*(t??1),this.a,this.b,this.opacity)},rgb(){var t=(this.l+16)/116,e=isNaN(this.a)?t:t+this.a/500,i=isNaN(this.b)?t:t-this.b/200;return e=he*Lt(e),t=me*Lt(t),i=ke*Lt(i),new le(At(3.1338561*e-1.6168667*t-.4906146*i),At(-.9787684*e+1.9161415*t+.033454*i),At(.0719453*e-.2289914*t+1.4052427*i),this.opacity)}}));function $t(t){return t>os?Math.pow(t,1/3):t/ge+ye}function Lt(t){return t>mt?t*t*t:ge*(t-ye)}function At(t){return 255*(t<=.0031308?12.92*t:1.055*Math.pow(t,1/2.4)-.055)}function Ft(t){return(t/=255)<=.04045?t/12.92:Math.pow((t+.055)/1.055,2.4)}function ls(t){if(t instanceof nt)return new nt(t.h,t.c,t.l,t.opacity);if(t instanceof st||(t=pe(t)),t.a===0&&t.b===0)return new nt(NaN,0<t.l&&t.l<100?0:NaN,t.l,t.opacity);var e=Math.atan2(t.b,t.a)*as;return new nt(e<0?e+360:e,Math.sqrt(t.a*t.a+t.b*t.b),t.l,t.opacity)}function Wt(t,e,i,s){return arguments.length===1?ls(t):new nt(t,e,i,s??1)}function nt(t,e,i,s){this.h=+t,this.c=+e,this.l=+i,this.opacity=+s}function ve(t){if(isNaN(t.h))return new st(t.l,0,0,t.opacity);var e=t.h*rs;return new st(t.l,Math.cos(e)*t.c,Math.sin(e)*t.c,t.opacity)}ue(nt,Wt,de(fe,{brighter(t){return new nt(this.h,this.c,this.l+Dt*(t??1),this.opacity)},darker(t){return new nt(this.h,this.c,this.l-Dt*(t??1),this.opacity)},rgb(){return ve(this).rgb()}}));function us(t){return function(e,i){var s=t((e=Wt(e)).h,(i=Wt(i)).h),n=Yt(e.c,i.c),y=Yt(e.l,i.l),g=Yt(e.opacity,i.opacity);return function(T){return e.h=s(T),e.c=n(T),e.l=y(T),e.opacity=g(T),e+""}}}const ds=us(Ge);var xe={exports:{}};(function(t,e){(function(i,s){t.exports=s()})(Mt,function(){var i="day";return function(s,n,y){var g=function(A){return A.add(4-A.isoWeekday(),i)},T=n.prototype;T.isoWeekYear=function(){return g(this).year()},T.isoWeek=function(A){if(!this.$utils().u(A))return this.add(7*(A-this.isoWeek()),i);var w,N,F,V,X=g(this),z=(w=this.isoWeekYear(),N=this.$u,F=(N?y.utc:y)().year(w).startOf("year"),V=4-F.isoWeekday(),F.isoWeekday()>4&&(V+=7),F.add(V,i));return X.diff(z,"week")+1},T.isoWeekday=function(A){return this.$utils().u(A)?this.day()||7:this.day(this.day()%7?A:A-7)};var L=T.startOf;T.startOf=function(A,w){var N=this.$utils(),F=!!N.u(w)||w;return N.p(A)==="isoweek"?F?this.date(this.date()-(this.isoWeekday()-1)).startOf("day"):this.date(this.date()-1-(this.isoWeekday()-1)+7).endOf("day"):L.bind(this)(A,w)}}})})(xe);var fs=xe.exports;const hs=Et(fs);var Te={exports:{}};(function(t,e){(function(i,s){t.exports=s()})(Mt,function(){var i={LTS:"h:mm:ss A",LT:"h:mm A",L:"MM/DD/YYYY",LL:"MMMM D, YYYY",LLL:"MMMM D, YYYY h:mm A",LLLL:"dddd, MMMM D, YYYY h:mm A"},s=/(\[[^[]*\])|([-_:/.,()\s]+)|(A|a|Q|YYYY|YY?|ww?|MM?M?M?|Do|DD?|hh?|HH?|mm?|ss?|S{1,3}|z|ZZ?)/g,n=/\d/,y=/\d\d/,g=/\d\d?/,T=/\d*[^-_:/,()\s\d]+/,L={},A=function(k){return(k=+k)+(k>68?1900:2e3)},w=function(k){return function(E){this[k]=+E}},N=[/[+-]\d\d:?(\d\d)?|Z/,function(k){(this.zone||(this.zone={})).offset=function(E){if(!E||E==="Z")return 0;var O=E.match(/([+-]|\d\d)/g),$=60*O[1]+(+O[2]||0);return $===0?0:O[0]==="+"?-$:$}(k)}],F=function(k){var E=L[k];return E&&(E.indexOf?E:E.s.concat(E.f))},V=function(k,E){var O,$=L.meridiem;if($){for(var q=1;q<=24;q+=1)if(k.indexOf($(q,0,E))>-1){O=q>12;break}}else O=k===(E?"pm":"PM");return O},X={A:[T,function(k){this.afternoon=V(k,!1)}],a:[T,function(k){this.afternoon=V(k,!0)}],Q:[n,function(k){this.month=3*(k-1)+1}],S:[n,function(k){this.milliseconds=100*+k}],SS:[y,function(k){this.milliseconds=10*+k}],SSS:[/\d{3}/,function(k){this.milliseconds=+k}],s:[g,w("seconds")],ss:[g,w("seconds")],m:[g,w("minutes")],mm:[g,w("minutes")],H:[g,w("hours")],h:[g,w("hours")],HH:[g,w("hours")],hh:[g,w("hours")],D:[g,w("day")],DD:[y,w("day")],Do:[T,function(k){var E=L.ordinal,O=k.match(/\d+/);if(this.day=O[0],E)for(var $=1;$<=31;$+=1)E($).replace(/\[|\]/g,"")===k&&(this.day=$)}],w:[g,w("week")],ww:[y,w("week")],M:[g,w("month")],MM:[y,w("month")],MMM:[T,function(k){var E=F("months"),O=(F("monthsShort")||E.map(function($){return $.slice(0,3)})).indexOf(k)+1;if(O<1)throw new Error;this.month=O%12||O}],MMMM:[T,function(k){var E=F("months").indexOf(k)+1;if(E<1)throw new Error;this.month=E%12||E}],Y:[/[+-]?\d+/,w("year")],YY:[y,function(k){this.year=A(k)}],YYYY:[/\d{4}/,w("year")],Z:N,ZZ:N};function z(k){var E,O;E=k,O=L&&L.formats;for(var $=(k=E.replace(/(\[[^\]]+])|(LTS?|l{1,4}|L{1,4})/g,function(m,x,v){var p=v&&v.toUpperCase();return x||O[v]||i[v]||O[p].replace(/(\[[^\]]+])|(MMMM|MM|DD|dddd)/g,function(a,d,f){return d||f.slice(1)})})).match(s),q=$.length,B=0;B<q;B+=1){var Y=$[B],b=X[Y],h=b&&b[0],I=b&&b[1];$[B]=I?{regex:h,parser:I}:Y.replace(/^\[|\]$/g,"")}return function(m){for(var x={},v=0,p=0;v<q;v+=1){var a=$[v];if(typeof a=="string")p+=a.length;else{var d=a.regex,f=a.parser,u=m.slice(p),_=d.exec(u)[0];f.call(x,_),m=m.replace(_,"")}}return function(r){var D=r.afternoon;if(D!==void 0){var o=r.hours;D?o<12&&(r.hours+=12):o===12&&(r.hours=0),delete r.afternoon}}(x),x}}return function(k,E,O){O.p.customParseFormat=!0,k&&k.parseTwoDigitYear&&(A=k.parseTwoDigitYear);var $=E.prototype,q=$.parse;$.parse=function(B){var Y=B.date,b=B.utc,h=B.args;this.$u=b;var I=h[1];if(typeof I=="string"){var m=h[2]===!0,x=h[3]===!0,v=m||x,p=h[2];x&&(p=h[2]),L=this.$locale(),!m&&p&&(L=O.Ls[p]),this.$d=function(u,_,r,D){try{if(["x","X"].indexOf(_)>-1)return new Date((_==="X"?1e3:1)*u);var o=z(_)(u),H=o.year,c=o.month,S=o.day,C=o.hours,P=o.minutes,M=o.seconds,R=o.milliseconds,W=o.zone,rt=o.week,ot=new Date,vt=S||(H||c?1:ot.getDate()),dt=H||ot.getFullYear(),G=0;H&&!c||(G=c>0?c-1:ot.getMonth());var K,Z=C||0,ct=P||0,J=M||0,at=R||0;return W?new Date(Date.UTC(dt,G,vt,Z,ct,J,at+60*W.offset*1e3)):r?new Date(Date.UTC(dt,G,vt,Z,ct,J,at)):(K=new Date(dt,G,vt,Z,ct,J,at),rt&&(K=D(K).week(rt).toDate()),K)}catch{return new Date("")}}(Y,I,b,O),this.init(),p&&p!==!0&&(this.$L=this.locale(p).$L),v&&Y!=this.format(I)&&(this.$d=new Date("")),L={}}else if(I instanceof Array)for(var a=I.length,d=1;d<=a;d+=1){h[1]=I[d-1];var f=O.apply(this,h);if(f.isValid()){this.$d=f.$d,this.$L=f.$L,this.init();break}d===a&&(this.$d=new Date(""))}else q.call(this,B)}}})})(Te);var ms=Te.exports;const ks=Et(ms);var be={exports:{}};(function(t,e){(function(i,s){t.exports=s()})(Mt,function(){return function(i,s){var n=s.prototype,y=n.format;n.format=function(g){var T=this,L=this.$locale();if(!this.isValid())return y.bind(this)(g);var A=this.$utils(),w=(g||"YYYY-MM-DDTHH:mm:ssZ").replace(/\[([^\]]+)]|Q|wo|ww|w|WW|W|zzz|z|gggg|GGGG|Do|X|x|k{1,2}|S/g,function(N){switch(N){case"Q":return Math.ceil((T.$M+1)/3);case"Do":return L.ordinal(T.$D);case"gggg":return T.weekYear();case"GGGG":return T.isoWeekYear();case"wo":return L.ordinal(T.week(),"W");case"w":case"ww":return A.s(T.week(),N==="w"?1:2,"0");case"W":case"WW":return A.s(T.isoWeek(),N==="W"?1:2,"0");case"k":case"kk":return A.s(String(T.$H===0?24:T.$H),N==="k"?1:2,"0");case"X":return Math.floor(T.$d.getTime()/1e3);case"x":return T.$d.getTime();case"z":return"["+T.offsetName()+"]";case"zzz":return"["+T.offsetName("long")+"]";default:return N}});return y.bind(this)(w)}}})})(be);var ys=be.exports;const gs=Et(ys);var we={exports:{}};(function(t,e){(function(i,s){t.exports=s()})(Mt,function(){var i,s,n=1e3,y=6e4,g=36e5,T=864e5,L=31536e6,A=2628e6,w=/^(-|\+)?P(?:([-+]?[0-9,.]*)Y)?(?:([-+]?[0-9,.]*)M)?(?:([-+]?[0-9,.]*)W)?(?:([-+]?[0-9,.]*)D)?(?:T(?:([-+]?[0-9,.]*)H)?(?:([-+]?[0-9,.]*)M)?(?:([-+]?[0-9,.]*)S)?)?$/,N=/\[([^\]]+)]|YYYY|YY|Y|M{1,2}|D{1,2}|H{1,2}|m{1,2}|s{1,2}|SSS/g,F={years:L,months:A,days:T,hours:g,minutes:y,seconds:n,milliseconds:1,weeks:6048e5},V=function(Y){return Y instanceof q},X=function(Y,b,h){return new q(Y,h,b.$l)},z=function(Y){return s.p(Y)+"s"},k=function(Y){return Y<0},E=function(Y){return k(Y)?Math.ceil(Y):Math.floor(Y)},O=function(Y){return Math.abs(Y)},$=function(Y,b){return Y?k(Y)?{negative:!0,format:""+O(Y)+b}:{negative:!1,format:""+Y+b}:{negative:!1,format:""}},q=function(){function Y(h,I,m){var x=this;if(this.$d={},this.$l=m,h===void 0&&(this.$ms=0,this.parseFromMilliseconds()),I)return X(h*F[z(I)],this);if(typeof h=="number")return this.$ms=h,this.parseFromMilliseconds(),this;if(typeof h=="object")return Object.keys(h).forEach(function(a){x.$d[z(a)]=h[a]}),this.calMilliseconds(),this;if(typeof h=="string"){var v=h.match(w);if(v){var p=v.slice(2).map(function(a){return a!=null?Number(a):0});return this.$d.years=p[0],this.$d.months=p[1],this.$d.weeks=p[2],this.$d.days=p[3],this.$d.hours=p[4],this.$d.minutes=p[5],this.$d.seconds=p[6],this.calMilliseconds(),this}}return this}var b=Y.prototype;return b.calMilliseconds=function(){var h=this;this.$ms=Object.keys(this.$d).reduce(function(I,m){return I+(h.$d[m]||0)*F[m]},0)},b.parseFromMilliseconds=function(){var h=this.$ms;this.$d.years=E(h/L),h%=L,this.$d.months=E(h/A),h%=A,this.$d.days=E(h/T),h%=T,this.$d.hours=E(h/g),h%=g,this.$d.minutes=E(h/y),h%=y,this.$d.seconds=E(h/n),h%=n,this.$d.milliseconds=h},b.toISOString=function(){var h=$(this.$d.years,"Y"),I=$(this.$d.months,"M"),m=+this.$d.days||0;this.$d.weeks&&(m+=7*this.$d.weeks);var x=$(m,"D"),v=$(this.$d.hours,"H"),p=$(this.$d.minutes,"M"),a=this.$d.seconds||0;this.$d.milliseconds&&(a+=this.$d.milliseconds/1e3,a=Math.round(1e3*a)/1e3);var d=$(a,"S"),f=h.negative||I.negative||x.negative||v.negative||p.negative||d.negative,u=v.format||p.format||d.format?"T":"",_=(f?"-":"")+"P"+h.format+I.format+x.format+u+v.format+p.format+d.format;return _==="P"||_==="-P"?"P0D":_},b.toJSON=function(){return this.toISOString()},b.format=function(h){var I=h||"YYYY-MM-DDTHH:mm:ss",m={Y:this.$d.years,YY:s.s(this.$d.years,2,"0"),YYYY:s.s(this.$d.years,4,"0"),M:this.$d.months,MM:s.s(this.$d.months,2,"0"),D:this.$d.days,DD:s.s(this.$d.days,2,"0"),H:this.$d.hours,HH:s.s(this.$d.hours,2,"0"),m:this.$d.minutes,mm:s.s(this.$d.minutes,2,"0"),s:this.$d.seconds,ss:s.s(this.$d.seconds,2,"0"),SSS:s.s(this.$d.milliseconds,3,"0")};return I.replace(N,function(x,v){return v||String(m[x])})},b.as=function(h){return this.$ms/F[z(h)]},b.get=function(h){var I=this.$ms,m=z(h);return m==="milliseconds"?I%=1e3:I=m==="weeks"?E(I/F[m]):this.$d[m],I||0},b.add=function(h,I,m){var x;return x=I?h*F[z(I)]:V(h)?h.$ms:X(h,this).$ms,X(this.$ms+x*(m?-1:1),this)},b.subtract=function(h,I){return this.add(h,I,!0)},b.locale=function(h){var I=this.clone();return I.$l=h,I},b.clone=function(){return X(this.$ms,this)},b.humanize=function(h){return i().add(this.$ms,"ms").locale(this.$l).fromNow(!h)},b.valueOf=function(){return this.asMilliseconds()},b.milliseconds=function(){return this.get("milliseconds")},b.asMilliseconds=function(){return this.as("milliseconds")},b.seconds=function(){return this.get("seconds")},b.asSeconds=function(){return this.as("seconds")},b.minutes=function(){return this.get("minutes")},b.asMinutes=function(){return this.as("minutes")},b.hours=function(){return this.get("hours")},b.asHours=function(){return this.as("hours")},b.days=function(){return this.get("days")},b.asDays=function(){return this.as("days")},b.weeks=function(){return this.get("weeks")},b.asWeeks=function(){return this.as("weeks")},b.months=function(){return this.get("months")},b.asMonths=function(){return this.as("months")},b.years=function(){return this.get("years")},b.asYears=function(){return this.as("years")},Y}(),B=function(Y,b,h){return Y.add(b.years()*h,"y").add(b.months()*h,"M").add(b.days()*h,"d").add(b.hours()*h,"h").add(b.minutes()*h,"m").add(b.seconds()*h,"s").add(b.milliseconds()*h,"ms")};return function(Y,b,h){i=h,s=h().$utils(),h.duration=function(x,v){var p=h.locale();return X(x,{$l:p},v)},h.isDuration=V;var I=b.prototype.add,m=b.prototype.subtract;b.prototype.add=function(x,v){return V(x)?B(this,x,1):I.bind(this)(x,v)},b.prototype.subtract=function(x,v){return V(x)?B(this,x,-1):m.bind(this)(x,v)}}})})(we);var ps=we.exports;const vs=Et(ps);var Nt=function(){var t=l(function(p,a,d,f){for(d=d||{},f=p.length;f--;d[p[f]]=a);return d},"o"),e=[6,8,10,12,13,14,15,16,17,18,20,21,22,23,24,25,26,27,28,29,30,31,33,35,36,38,40],i=[1,26],s=[1,27],n=[1,28],y=[1,29],g=[1,30],T=[1,31],L=[1,32],A=[1,33],w=[1,34],N=[1,9],F=[1,10],V=[1,11],X=[1,12],z=[1,13],k=[1,14],E=[1,15],O=[1,16],$=[1,19],q=[1,20],B=[1,21],Y=[1,22],b=[1,23],h=[1,25],I=[1,35],m={trace:l(function(){},"trace"),yy:{},symbols_:{error:2,start:3,gantt:4,document:5,EOF:6,line:7,SPACE:8,statement:9,NL:10,weekday:11,weekday_monday:12,weekday_tuesday:13,weekday_wednesday:14,weekday_thursday:15,weekday_friday:16,weekday_saturday:17,weekday_sunday:18,weekend:19,weekend_friday:20,weekend_saturday:21,dateFormat:22,inclusiveEndDates:23,topAxis:24,axisFormat:25,tickInterval:26,excludes:27,includes:28,todayMarker:29,title:30,acc_title:31,acc_title_value:32,acc_descr:33,acc_descr_value:34,acc_descr_multiline_value:35,section:36,clickStatement:37,taskTxt:38,taskData:39,click:40,callbackname:41,callbackargs:42,href:43,clickStatementDebug:44,$accept:0,$end:1},terminals_:{2:"error",4:"gantt",6:"EOF",8:"SPACE",10:"NL",12:"weekday_monday",13:"weekday_tuesday",14:"weekday_wednesday",15:"weekday_thursday",16:"weekday_friday",17:"weekday_saturday",18:"weekday_sunday",20:"weekend_friday",21:"weekend_saturday",22:"dateFormat",23:"inclusiveEndDates",24:"topAxis",25:"axisFormat",26:"tickInterval",27:"excludes",28:"includes",29:"todayMarker",30:"title",31:"acc_title",32:"acc_title_value",33:"acc_descr",34:"acc_descr_value",35:"acc_descr_multiline_value",36:"section",38:"taskTxt",39:"taskData",40:"click",41:"callbackname",42:"callbackargs",43:"href"},productions_:[0,[3,3],[5,0],[5,2],[7,2],[7,1],[7,1],[7,1],[11,1],[11,1],[11,1],[11,1],[11,1],[11,1],[11,1],[19,1],[19,1],[9,1],[9,1],[9,1],[9,1],[9,1],[9,1],[9,1],[9,1],[9,1],[9,1],[9,1],[9,2],[9,2],[9,1],[9,1],[9,1],[9,2],[37,2],[37,3],[37,3],[37,4],[37,3],[37,4],[37,2],[44,2],[44,3],[44,3],[44,4],[44,3],[44,4],[44,2]],performAction:l(function(a,d,f,u,_,r,D){var o=r.length-1;switch(_){case 1:return r[o-1];case 2:this.$=[];break;case 3:r[o-1].push(r[o]),this.$=r[o-1];break;case 4:case 5:this.$=r[o];break;case 6:case 7:this.$=[];break;case 8:u.setWeekday("monday");break;case 9:u.setWeekday("tuesday");break;case 10:u.setWeekday("wednesday");break;case 11:u.setWeekday("thursday");break;case 12:u.setWeekday("friday");break;case 13:u.setWeekday("saturday");break;case 14:u.setWeekday("sunday");break;case 15:u.setWeekend("friday");break;case 16:u.setWeekend("saturday");break;case 17:u.setDateFormat(r[o].substr(11)),this.$=r[o].substr(11);break;case 18:u.enableInclusiveEndDates(),this.$=r[o].substr(18);break;case 19:u.TopAxis(),this.$=r[o].substr(8);break;case 20:u.setAxisFormat(r[o].substr(11)),this.$=r[o].substr(11);break;case 21:u.setTickInterval(r[o].substr(13)),this.$=r[o].substr(13);break;case 22:u.setExcludes(r[o].substr(9)),this.$=r[o].substr(9);break;case 23:u.setIncludes(r[o].substr(9)),this.$=r[o].substr(9);break;case 24:u.setTodayMarker(r[o].substr(12)),this.$=r[o].substr(12);break;case 27:u.setDiagramTitle(r[o].substr(6)),this.$=r[o].substr(6);break;case 28:this.$=r[o].trim(),u.setAccTitle(this.$);break;case 29:case 30:this.$=r[o].trim(),u.setAccDescription(this.$);break;case 31:u.addSection(r[o].substr(8)),this.$=r[o].substr(8);break;case 33:u.addTask(r[o-1],r[o]),this.$="task";break;case 34:this.$=r[o-1],u.setClickEvent(r[o-1],r[o],null);break;case 35:this.$=r[o-2],u.setClickEvent(r[o-2],r[o-1],r[o]);break;case 36:this.$=r[o-2],u.setClickEvent(r[o-2],r[o-1],null),u.setLink(r[o-2],r[o]);break;case 37:this.$=r[o-3],u.setClickEvent(r[o-3],r[o-2],r[o-1]),u.setLink(r[o-3],r[o]);break;case 38:this.$=r[o-2],u.setClickEvent(r[o-2],r[o],null),u.setLink(r[o-2],r[o-1]);break;case 39:this.$=r[o-3],u.setClickEvent(r[o-3],r[o-1],r[o]),u.setLink(r[o-3],r[o-2]);break;case 40:this.$=r[o-1],u.setLink(r[o-1],r[o]);break;case 41:case 47:this.$=r[o-1]+" "+r[o];break;case 42:case 43:case 45:this.$=r[o-2]+" "+r[o-1]+" "+r[o];break;case 44:case 46:this.$=r[o-3]+" "+r[o-2]+" "+r[o-1]+" "+r[o];break}},"anonymous"),table:[{3:1,4:[1,2]},{1:[3]},t(e,[2,2],{5:3}),{6:[1,4],7:5,8:[1,6],9:7,10:[1,8],11:17,12:i,13:s,14:n,15:y,16:g,17:T,18:L,19:18,20:A,21:w,22:N,23:F,24:V,25:X,26:z,27:k,28:E,29:O,30:$,31:q,33:B,35:Y,36:b,37:24,38:h,40:I},t(e,[2,7],{1:[2,1]}),t(e,[2,3]),{9:36,11:17,12:i,13:s,14:n,15:y,16:g,17:T,18:L,19:18,20:A,21:w,22:N,23:F,24:V,25:X,26:z,27:k,28:E,29:O,30:$,31:q,33:B,35:Y,36:b,37:24,38:h,40:I},t(e,[2,5]),t(e,[2,6]),t(e,[2,17]),t(e,[2,18]),t(e,[2,19]),t(e,[2,20]),t(e,[2,21]),t(e,[2,22]),t(e,[2,23]),t(e,[2,24]),t(e,[2,25]),t(e,[2,26]),t(e,[2,27]),{32:[1,37]},{34:[1,38]},t(e,[2,30]),t(e,[2,31]),t(e,[2,32]),{39:[1,39]},t(e,[2,8]),t(e,[2,9]),t(e,[2,10]),t(e,[2,11]),t(e,[2,12]),t(e,[2,13]),t(e,[2,14]),t(e,[2,15]),t(e,[2,16]),{41:[1,40],43:[1,41]},t(e,[2,4]),t(e,[2,28]),t(e,[2,29]),t(e,[2,33]),t(e,[2,34],{42:[1,42],43:[1,43]}),t(e,[2,40],{41:[1,44]}),t(e,[2,35],{43:[1,45]}),t(e,[2,36]),t(e,[2,38],{42:[1,46]}),t(e,[2,37]),t(e,[2,39])],defaultActions:{},parseError:l(function(a,d){if(d.recoverable)this.trace(a);else{var f=new Error(a);throw f.hash=d,f}},"parseError"),parse:l(function(a){var d=this,f=[0],u=[],_=[null],r=[],D=this.table,o="",H=0,c=0,S=2,C=1,P=r.slice.call(arguments,1),M=Object.create(this.lexer),R={yy:{}};for(var W in this.yy)Object.prototype.hasOwnProperty.call(this.yy,W)&&(R.yy[W]=this.yy[W]);M.setInput(a,R.yy),R.yy.lexer=M,R.yy.parser=this,typeof M.yylloc>"u"&&(M.yylloc={});var rt=M.yylloc;r.push(rt);var ot=M.options&&M.options.ranges;typeof R.yy.parseError=="function"?this.parseError=R.yy.parseError:this.parseError=Object.getPrototypeOf(this).parseError;function vt(Q){f.length=f.length-2*Q,_.length=_.length-Q,r.length=r.length-Q}l(vt,"popStack");function dt(){var Q;return Q=u.pop()||M.lex()||C,typeof Q!="number"&&(Q instanceof Array&&(u=Q,Q=u.pop()),Q=d.symbols_[Q]||Q),Q}l(dt,"lex");for(var G,K,Z,ct,J={},at,tt,Kt,Tt;;){if(K=f[f.length-1],this.defaultActions[K]?Z=this.defaultActions[K]:((G===null||typeof G>"u")&&(G=dt()),Z=D[K]&&D[K][G]),typeof Z>"u"||!Z.length||!Z[0]){var It="";Tt=[];for(at in D[K])this.terminals_[at]&&at>S&&Tt.push("'"+this.terminals_[at]+"'");M.showPosition?It="Parse error on line "+(H+1)+`:
`+M.showPosition()+`
Expecting `+Tt.join(", ")+", got '"+(this.terminals_[G]||G)+"'":It="Parse error on line "+(H+1)+": Unexpected "+(G==C?"end of input":"'"+(this.terminals_[G]||G)+"'"),this.parseError(It,{text:M.match,token:this.terminals_[G]||G,line:M.yylineno,loc:rt,expected:Tt})}if(Z[0]instanceof Array&&Z.length>1)throw new Error("Parse Error: multiple actions possible at state: "+K+", token: "+G);switch(Z[0]){case 1:f.push(G),_.push(M.yytext),r.push(M.yylloc),f.push(Z[1]),G=null,c=M.yyleng,o=M.yytext,H=M.yylineno,rt=M.yylloc;break;case 2:if(tt=this.productions_[Z[1]][1],J.$=_[_.length-tt],J._$={first_line:r[r.length-(tt||1)].first_line,last_line:r[r.length-1].last_line,first_column:r[r.length-(tt||1)].first_column,last_column:r[r.length-1].last_column},ot&&(J._$.range=[r[r.length-(tt||1)].range[0],r[r.length-1].range[1]]),ct=this.performAction.apply(J,[o,c,H,R.yy,Z[1],_,r].concat(P)),typeof ct<"u")return ct;tt&&(f=f.slice(0,-1*tt*2),_=_.slice(0,-1*tt),r=r.slice(0,-1*tt)),f.push(this.productions_[Z[1]][0]),_.push(J.$),r.push(J._$),Kt=D[f[f.length-2]][f[f.length-1]],f.push(Kt);break;case 3:return!0}}return!0},"parse")},x=function(){var p={EOF:1,parseError:l(function(d,f){if(this.yy.parser)this.yy.parser.parseError(d,f);else throw new Error(d)},"parseError"),setInput:l(function(a,d){return this.yy=d||this.yy||{},this._input=a,this._more=this._backtrack=this.done=!1,this.yylineno=this.yyleng=0,this.yytext=this.matched=this.match="",this.conditionStack=["INITIAL"],this.yylloc={first_line:1,first_column:0,last_line:1,last_column:0},this.options.ranges&&(this.yylloc.range=[0,0]),this.offset=0,this},"setInput"),input:l(function(){var a=this._input[0];this.yytext+=a,this.yyleng++,this.offset++,this.match+=a,this.matched+=a;var d=a.match(/(?:\r\n?|\n).*/g);return d?(this.yylineno++,this.yylloc.last_line++):this.yylloc.last_column++,this.options.ranges&&this.yylloc.range[1]++,this._input=this._input.slice(1),a},"input"),unput:l(function(a){var d=a.length,f=a.split(/(?:\r\n?|\n)/g);this._input=a+this._input,this.yytext=this.yytext.substr(0,this.yytext.length-d),this.offset-=d;var u=this.match.split(/(?:\r\n?|\n)/g);this.match=this.match.substr(0,this.match.length-1),this.matched=this.matched.substr(0,this.matched.length-1),f.length-1&&(this.yylineno-=f.length-1);var _=this.yylloc.range;return this.yylloc={first_line:this.yylloc.first_line,last_line:this.yylineno+1,first_column:this.yylloc.first_column,last_column:f?(f.length===u.length?this.yylloc.first_column:0)+u[u.length-f.length].length-f[0].length:this.yylloc.first_column-d},this.options.ranges&&(this.yylloc.range=[_[0],_[0]+this.yyleng-d]),this.yyleng=this.yytext.length,this},"unput"),more:l(function(){return this._more=!0,this},"more"),reject:l(function(){if(this.options.backtrack_lexer)this._backtrack=!0;else return this.parseError("Lexical error on line "+(this.yylineno+1)+`. You can only invoke reject() in the lexer when the lexer is of the backtracking persuasion (options.backtrack_lexer = true).
`+this.showPosition(),{text:"",token:null,line:this.yylineno});return this},"reject"),less:l(function(a){this.unput(this.match.slice(a))},"less"),pastInput:l(function(){var a=this.matched.substr(0,this.matched.length-this.match.length);return(a.length>20?"...":"")+a.substr(-20).replace(/\n/g,"")},"pastInput"),upcomingInput:l(function(){var a=this.match;return a.length<20&&(a+=this._input.substr(0,20-a.length)),(a.substr(0,20)+(a.length>20?"...":"")).replace(/\n/g,"")},"upcomingInput"),showPosition:l(function(){var a=this.pastInput(),d=new Array(a.length+1).join("-");return a+this.upcomingInput()+`
`+d+"^"},"showPosition"),test_match:l(function(a,d){var f,u,_;if(this.options.backtrack_lexer&&(_={yylineno:this.yylineno,yylloc:{first_line:this.yylloc.first_line,last_line:this.last_line,first_column:this.yylloc.first_column,last_column:this.yylloc.last_column},yytext:this.yytext,match:this.match,matches:this.matches,matched:this.matched,yyleng:this.yyleng,offset:this.offset,_more:this._more,_input:this._input,yy:this.yy,conditionStack:this.conditionStack.slice(0),done:this.done},this.options.ranges&&(_.yylloc.range=this.yylloc.range.slice(0))),u=a[0].match(/(?:\r\n?|\n).*/g),u&&(this.yylineno+=u.length),this.yylloc={first_line:this.yylloc.last_line,last_line:this.yylineno+1,first_column:this.yylloc.last_column,last_column:u?u[u.length-1].length-u[u.length-1].match(/\r?\n?/)[0].length:this.yylloc.last_column+a[0].length},this.yytext+=a[0],this.match+=a[0],this.matches=a,this.yyleng=this.yytext.length,this.options.ranges&&(this.yylloc.range=[this.offset,this.offset+=this.yyleng]),this._more=!1,this._backtrack=!1,this._input=this._input.slice(a[0].length),this.matched+=a[0],f=this.performAction.call(this,this.yy,this,d,this.conditionStack[this.conditionStack.length-1]),this.done&&this._input&&(this.done=!1),f)return f;if(this._backtrack){for(var r in _)this[r]=_[r];return!1}return!1},"test_match"),next:l(function(){if(this.done)return this.EOF;this._input||(this.done=!0);var a,d,f,u;this._more||(this.yytext="",this.match="");for(var _=this._currentRules(),r=0;r<_.length;r++)if(f=this._input.match(this.rules[_[r]]),f&&(!d||f[0].length>d[0].length)){if(d=f,u=r,this.options.backtrack_lexer){if(a=this.test_match(f,_[r]),a!==!1)return a;if(this._backtrack){d=!1;continue}else return!1}else if(!this.options.flex)break}return d?(a=this.test_match(d,_[u]),a!==!1?a:!1):this._input===""?this.EOF:this.parseError("Lexical error on line "+(this.yylineno+1)+`. Unrecognized text.
`+this.showPosition(),{text:"",token:null,line:this.yylineno})},"next"),lex:l(function(){var d=this.next();return d||this.lex()},"lex"),begin:l(function(d){this.conditionStack.push(d)},"begin"),popState:l(function(){var d=this.conditionStack.length-1;return d>0?this.conditionStack.pop():this.conditionStack[0]},"popState"),_currentRules:l(function(){return this.conditionStack.length&&this.conditionStack[this.conditionStack.length-1]?this.conditions[this.conditionStack[this.conditionStack.length-1]].rules:this.conditions.INITIAL.rules},"_currentRules"),topState:l(function(d){return d=this.conditionStack.length-1-Math.abs(d||0),d>=0?this.conditionStack[d]:"INITIAL"},"topState"),pushState:l(function(d){this.begin(d)},"pushState"),stateStackSize:l(function(){return this.conditionStack.length},"stateStackSize"),options:{"case-insensitive":!0},performAction:l(function(d,f,u,_){switch(u){case 0:return this.begin("open_directive"),"open_directive";case 1:return this.begin("acc_title"),31;case 2:return this.popState(),"acc_title_value";case 3:return this.begin("acc_descr"),33;case 4:return this.popState(),"acc_descr_value";case 5:this.begin("acc_descr_multiline");break;case 6:this.popState();break;case 7:return"acc_descr_multiline_value";case 8:break;case 9:break;case 10:break;case 11:return 10;case 12:break;case 13:break;case 14:this.begin("href");break;case 15:this.popState();break;case 16:return 43;case 17:this.begin("callbackname");break;case 18:this.popState();break;case 19:this.popState(),this.begin("callbackargs");break;case 20:return 41;case 21:this.popState();break;case 22:return 42;case 23:this.begin("click");break;case 24:this.popState();break;case 25:return 40;case 26:return 4;case 27:return 22;case 28:return 23;case 29:return 24;case 30:return 25;case 31:return 26;case 32:return 28;case 33:return 27;case 34:return 29;case 35:return 12;case 36:return 13;case 37:return 14;case 38:return 15;case 39:return 16;case 40:return 17;case 41:return 18;case 42:return 20;case 43:return 21;case 44:return"date";case 45:return 30;case 46:return"accDescription";case 47:return 36;case 48:return 38;case 49:return 39;case 50:return":";case 51:return 6;case 52:return"INVALID"}},"anonymous"),rules:[/^(?:%%\{)/i,/^(?:accTitle\s*:\s*)/i,/^(?:(?!\n||)*[^\n]*)/i,/^(?:accDescr\s*:\s*)/i,/^(?:(?!\n||)*[^\n]*)/i,/^(?:accDescr\s*\{\s*)/i,/^(?:[\}])/i,/^(?:[^\}]*)/i,/^(?:%%(?!\{)*[^\n]*)/i,/^(?:[^\}]%%*[^\n]*)/i,/^(?:%%*[^\n]*[\n]*)/i,/^(?:[\n]+)/i,/^(?:\s+)/i,/^(?:%[^\n]*)/i,/^(?:href[\s]+["])/i,/^(?:["])/i,/^(?:[^"]*)/i,/^(?:call[\s]+)/i,/^(?:\([\s]*\))/i,/^(?:\()/i,/^(?:[^(]*)/i,/^(?:\))/i,/^(?:[^)]*)/i,/^(?:click[\s]+)/i,/^(?:[\s\n])/i,/^(?:[^\s\n]*)/i,/^(?:gantt\b)/i,/^(?:dateFormat\s[^#\n;]+)/i,/^(?:inclusiveEndDates\b)/i,/^(?:topAxis\b)/i,/^(?:axisFormat\s[^#\n;]+)/i,/^(?:tickInterval\s[^#\n;]+)/i,/^(?:includes\s[^#\n;]+)/i,/^(?:excludes\s[^#\n;]+)/i,/^(?:todayMarker\s[^\n;]+)/i,/^(?:weekday\s+monday\b)/i,/^(?:weekday\s+tuesday\b)/i,/^(?:weekday\s+wednesday\b)/i,/^(?:weekday\s+thursday\b)/i,/^(?:weekday\s+friday\b)/i,/^(?:weekday\s+saturday\b)/i,/^(?:weekday\s+sunday\b)/i,/^(?:weekend\s+friday\b)/i,/^(?:weekend\s+saturday\b)/i,/^(?:\d\d\d\d-\d\d-\d\d\b)/i,/^(?:title\s[^\n]+)/i,/^(?:accDescription\s[^#\n;]+)/i,/^(?:section\s[^\n]+)/i,/^(?:[^:\n]+)/i,/^(?::[^#\n;]+)/i,/^(?::)/i,/^(?:$)/i,/^(?:.)/i],conditions:{acc_descr_multiline:{rules:[6,7],inclusive:!1},acc_descr:{rules:[4],inclusive:!1},acc_title:{rules:[2],inclusive:!1},callbackargs:{rules:[21,22],inclusive:!1},callbackname:{rules:[18,19,20],inclusive:!1},href:{rules:[15,16],inclusive:!1},click:{rules:[24,25],inclusive:!1},INITIAL:{rules:[0,1,3,5,8,9,10,11,12,13,14,17,23,26,27,28,29,30,31,32,33,34,35,36,37,38,39,40,41,42,43,44,45,46,47,48,49,50,51,52],inclusive:!0}}};return p}();m.lexer=x;function v(){this.yy={}}return l(v,"Parser"),v.prototype=m,m.Parser=v,new v}();Nt.parser=Nt;var xs=Nt;U.extend(hs);U.extend(ks);U.extend(gs);var ae={friday:5,saturday:6},et="",zt="",Ht=void 0,Bt="",yt=[],gt=[],Gt=new Map,jt=[],St=[],pt="",Xt="",_e=["active","done","crit","milestone","vert"],qt=[],ft="",xt=!1,Ut=!1,Zt="sunday",Ct="saturday",Pt=0,Ts=l(function(){jt=[],St=[],pt="",qt=[],wt=0,Rt=void 0,_t=void 0,j=[],et="",zt="",Xt="",Ht=void 0,Bt="",yt=[],gt=[],xt=!1,Ut=!1,Pt=0,Gt=new Map,ft="",ze(),Zt="sunday",Ct="saturday"},"clear"),bs=l(function(t){ft=t},"setDiagramId"),ws=l(function(t){zt=t},"setAxisFormat"),_s=l(function(){return zt},"getAxisFormat"),Ds=l(function(t){Ht=t},"setTickInterval"),Ss=l(function(){return Ht},"getTickInterval"),Cs=l(function(t){Bt=t},"setTodayMarker"),Ms=l(function(){return Bt},"getTodayMarker"),Es=l(function(t){et=t},"setDateFormat"),Is=l(function(){xt=!0},"enableInclusiveEndDates"),Ys=l(function(){return xt},"endDatesAreInclusive"),$s=l(function(){Ut=!0},"enableTopAxis"),Ls=l(function(){return Ut},"topAxisEnabled"),As=l(function(t){Xt=t},"setDisplayMode"),Fs=l(function(){return Xt},"getDisplayMode"),Os=l(function(){return et},"getDateFormat"),De=l((t,e)=>{const i=e.toLowerCase().split(/[\s,]+/).filter(s=>s!=="");return[...new Set([...t,...i])]},"mergeTokens"),Ws=l(function(t){yt=De(yt,t)},"setIncludes"),Ns=l(function(){return yt},"getIncludes"),Ps=l(function(t){gt=De(gt,t)},"setExcludes"),Vs=l(function(){return gt},"getExcludes"),Rs=l(function(){return Gt},"getLinks"),zs=l(function(t){pt=t,jt.push(t)},"addSection"),Hs=l(function(){return jt},"getSections"),Bs=l(function(){let t=oe();const e=10;let i=0;for(;!t&&i<e;)t=oe(),i++;return St=j,St},"getTasks"),Se=l(function(t,e,i,s){const n=t.format(e.trim()),y=t.format("YYYY-MM-DD");return s.includes(n)||s.includes(y)?!1:i.includes("weekends")&&(t.isoWeekday()===ae[Ct]||t.isoWeekday()===ae[Ct]+1)||i.includes(t.format("dddd").toLowerCase())?!0:i.includes(n)||i.includes(y)},"isInvalidDate"),Gs=l(function(t){Zt=t},"setWeekday"),js=l(function(){return Zt},"getWeekday"),Xs=l(function(t){Ct=t},"setWeekend"),Ce=l(function(t,e,i,s){if(!i.length||t.manualEndTime)return;let n;t.startTime instanceof Date?n=U(t.startTime):n=U(t.startTime,e,!0),n=n.add(1,"d");let y;t.endTime instanceof Date?y=U(t.endTime):y=U(t.endTime,e,!0);const[g,T]=qs(n,y,e,i,s);t.endTime=g.toDate(),t.renderEndTime=T},"checkTaskDates"),qs=l(function(t,e,i,s,n){let y=!1,g=null;const T=e.add(1e4,"d");for(;t<=e;){if(y||(g=e.toDate()),y=Se(t,i,s,n),y&&(e=e.add(1,"d"),e>T))throw new Error("Failed to find a valid date that was not excluded by `excludes` after 10,000 iterations.");t=t.add(1,"d")}return[e,g]},"fixTaskDates"),Vt=l(function(t,e,i){if(i=i.trim(),l(T=>{const L=T.trim();return L==="x"||L==="X"},"isTimestampFormat")(e)&&/^\d+$/.test(i))return new Date(Number(i));const y=/^after\s+(?<ids>[\d\w- ]+)/.exec(i);if(y!==null){let T=null;for(const A of y.groups.ids.split(" ")){let w=ut(A);w!==void 0&&(!T||w.endTime>T.endTime)&&(T=w)}if(T)return T.endTime;const L=new Date;return L.setHours(0,0,0,0),L}let g=U(i,e.trim(),!0);if(g.isValid())return g.toDate();{lt.debug("Invalid date:"+i),lt.debug("With date format:"+e.trim());const T=new Date(i);if(T===void 0||isNaN(T.getTime())||T.getFullYear()<-1e4||T.getFullYear()>1e4)throw new Error("Invalid date:"+i);return T}},"getStartDate"),Me=l(function(t){const e=/^(\d+(?:\.\d+)?)([Mdhmswy]|ms)$/.exec(t.trim());return e!==null?[Number.parseFloat(e[1]),e[2]]:[NaN,"ms"]},"parseDuration"),Ee=l(function(t,e,i,s=!1){i=i.trim();const y=/^until\s+(?<ids>[\d\w- ]+)/.exec(i);if(y!==null){let w=null;for(const F of y.groups.ids.split(" ")){let V=ut(F);V!==void 0&&(!w||V.startTime<w.startTime)&&(w=V)}if(w)return w.startTime;const N=new Date;return N.setHours(0,0,0,0),N}let g=U(i,e.trim(),!0);if(g.isValid())return s&&(g=g.add(1,"d")),g.toDate();let T=U(t);const[L,A]=Me(i);if(!Number.isNaN(L)){const w=T.add(L,A);w.isValid()&&(T=w)}return T.toDate()},"getEndDate"),wt=0,kt=l(function(t){return t===void 0?(wt=wt+1,"task"+wt):t},"parseId"),Us=l(function(t,e){let i;e.substr(0,1)===":"?i=e.substr(1,e.length):i=e;const s=i.split(","),n={};Qt(s,n,_e);for(let g=0;g<s.length;g++)s[g]=s[g].trim();let y="";switch(s.length){case 1:n.id=kt(),n.startTime=t.endTime,y=s[0];break;case 2:n.id=kt(),n.startTime=Vt(void 0,et,s[0]),y=s[1];break;case 3:n.id=kt(s[0]),n.startTime=Vt(void 0,et,s[1]),y=s[2];break}return y&&(n.endTime=Ee(n.startTime,et,y,xt),n.manualEndTime=U(y,"YYYY-MM-DD",!0).isValid(),Ce(n,et,gt,yt)),n},"compileData"),Zs=l(function(t,e){let i;e.substr(0,1)===":"?i=e.substr(1,e.length):i=e;const s=i.split(","),n={};Qt(s,n,_e);for(let y=0;y<s.length;y++)s[y]=s[y].trim();switch(s.length){case 1:n.id=kt(),n.startTime={type:"prevTaskEnd",id:t},n.endTime={data:s[0]};break;case 2:n.id=kt(),n.startTime={type:"getStartDate",startData:s[0]},n.endTime={data:s[1]};break;case 3:n.id=kt(s[0]),n.startTime={type:"getStartDate",startData:s[1]},n.endTime={data:s[2]};break}return n},"parseData"),Rt,_t,j=[],Ie={},Qs=l(function(t,e){const i={section:pt,type:pt,processed:!1,manualEndTime:!1,renderEndTime:null,raw:{data:e},task:t,classes:[]},s=Zs(_t,e);i.raw.startTime=s.startTime,i.raw.endTime=s.endTime,i.id=s.id,i.prevTaskId=_t,i.active=s.active,i.done=s.done,i.crit=s.crit,i.milestone=s.milestone,i.vert=s.vert,i.vert?i.order=-1:(i.order=Pt,Pt++);const n=j.push(i);_t=i.id,Ie[i.id]=n-1},"addTask"),ut=l(function(t){const e=Ie[t];return j[e]},"findTaskById"),Ks=l(function(t,e){const i={section:pt,type:pt,description:t,task:t,classes:[]},s=Us(Rt,e);i.startTime=s.startTime,i.endTime=s.endTime,i.id=s.id,i.active=s.active,i.done=s.done,i.crit=s.crit,i.milestone=s.milestone,i.vert=s.vert,Rt=i,St.push(i)},"addTaskOrg"),oe=l(function(){const t=l(function(i){const s=j[i];let n="";switch(j[i].raw.startTime.type){case"prevTaskEnd":{const y=ut(s.prevTaskId);s.startTime=y.endTime;break}case"getStartDate":n=Vt(void 0,et,j[i].raw.startTime.startData),n&&(j[i].startTime=n);break}return j[i].startTime&&(j[i].endTime=Ee(j[i].startTime,et,j[i].raw.endTime.data,xt),j[i].endTime&&(j[i].processed=!0,j[i].manualEndTime=U(j[i].raw.endTime.data,"YYYY-MM-DD",!0).isValid(),Ce(j[i],et,gt,yt))),j[i].processed},"compileTask");let e=!0;for(const[i,s]of j.entries())t(i),e=e&&s.processed;return e},"compileTasks"),Js=l(function(t,e){let i=e;ht().securityLevel!=="loose"&&(i=Re(e)),t.split(",").forEach(function(s){ut(s)!==void 0&&($e(s,()=>{window.open(i,"_self")}),Gt.set(s,i))}),Ye(t,"clickable")},"setLink"),Ye=l(function(t,e){t.split(",").forEach(function(i){let s=ut(i);s!==void 0&&s.classes.push(e)})},"setClass"),ti=l(function(t,e,i){if(ht().securityLevel!=="loose"||e===void 0)return;let s=[];if(typeof i=="string"){s=i.split(/,(?=(?:(?:[^"]*"){2})*[^"]*$)/);for(let y=0;y<s.length;y++){let g=s[y].trim();g.startsWith('"')&&g.endsWith('"')&&(g=g.substr(1,g.length-2)),s[y]=g}}s.length===0&&s.push(t),ut(t)!==void 0&&$e(t,()=>{He.runFunc(e,...s)})},"setClickFun"),$e=l(function(t,e){qt.push(function(){const i=ft?`${ft}-${t}`:t,s=document.querySelector(`[id="${i}"]`);s!==null&&s.addEventListener("click",function(){e()})},function(){const i=ft?`${ft}-${t}`:t,s=document.querySelector(`[id="${i}-text"]`);s!==null&&s.addEventListener("click",function(){e()})})},"pushFun"),ei=l(function(t,e,i){t.split(",").forEach(function(s){ti(s,e,i)}),Ye(t,"clickable")},"setClickEvent"),si=l(function(t){qt.forEach(function(e){e(t)})},"bindFunctions"),ii={getConfig:l(()=>ht().gantt,"getConfig"),clear:Ts,setDateFormat:Es,getDateFormat:Os,enableInclusiveEndDates:Is,endDatesAreInclusive:Ys,enableTopAxis:$s,topAxisEnabled:Ls,setAxisFormat:ws,getAxisFormat:_s,setTickInterval:Ds,getTickInterval:Ss,setTodayMarker:Cs,getTodayMarker:Ms,setAccTitle:Ne,getAccTitle:We,setDiagramTitle:Oe,getDiagramTitle:Fe,setDiagramId:bs,setDisplayMode:As,getDisplayMode:Fs,setAccDescription:Ae,getAccDescription:Le,addSection:zs,getSections:Hs,getTasks:Bs,addTask:Qs,findTaskById:ut,addTaskOrg:Ks,setIncludes:Ws,getIncludes:Ns,setExcludes:Ps,getExcludes:Vs,setClickEvent:ei,setLink:Js,getLinks:Rs,bindFunctions:si,parseDuration:Me,isInvalidDate:Se,setWeekday:Gs,getWeekday:js,setWeekend:Xs};function Qt(t,e,i){let s=!0;for(;s;)s=!1,i.forEach(function(n){const y="^\\s*"+n+"\\s*$",g=new RegExp(y);t[0].match(g)&&(e[n]=!0,t.shift(1),s=!0)})}l(Qt,"getTaskTags");U.extend(vs);var ni=l(function(){lt.debug("Something is calling, setConf, remove the call")},"setConf"),ce={monday:ts,tuesday:Je,wednesday:Ke,thursday:Qe,friday:Ze,saturday:Ue,sunday:qe},ri=l((t,e)=>{let i=[...t].map(()=>-1/0),s=[...t].sort((y,g)=>y.startTime-g.startTime||y.order-g.order),n=0;for(const y of s)for(let g=0;g<i.length;g++)if(y.startTime>=i[g]){i[g]=y.endTime,y.order=g+e,g>n&&(n=g);break}return n},"getMaxIntersections"),it,Ot=1e4,ai=l(function(t,e,i,s){const n=ht().gantt;s.db.setDiagramId(e);const y=ht().securityLevel;let g;y==="sandbox"&&(g=bt("#i"+e));const T=y==="sandbox"?bt(g.nodes()[0].contentDocument.body):bt("body"),L=y==="sandbox"?g.nodes()[0].contentDocument:document,A=L.getElementById(e);it=A.parentElement.offsetWidth,it===void 0&&(it=1200),n.useWidth!==void 0&&(it=n.useWidth);const w=s.db.getTasks(),N=w.filter(m=>!m.vert);let F=[];for(const m of N)F.push(m.type);F=I(F);const V={};let X=2*n.topPadding;if(s.db.getDisplayMode()==="compact"||n.displayMode==="compact"){const m={};for(const v of N)m[v.section]===void 0?m[v.section]=[v]:m[v.section].push(v);let x=0;for(const v of Object.keys(m)){const p=ri(m[v],x)+1;x+=p,X+=p*(n.barHeight+n.barGap),V[v]=p}}else{X+=N.length*(n.barHeight+n.barGap);for(const m of F)V[m]=N.filter(x=>x.type===m).length}A.setAttribute("viewBox","0 0 "+it+" "+X);const z=T.select(`[id="${e}"]`),k=je().domain([ns(w,function(m){return m.startTime}),is(w,function(m){return m.endTime})]).rangeRound([0,it-n.leftPadding-n.rightPadding]);function E(m,x){const v=m.startTime,p=x.startTime;let a=0;return v>p?a=1:v<p&&(a=-1),a}l(E,"taskCompare"),w.sort(E),O(w,it,X),Pe(z,X,it,n.useMaxWidth),z.append("text").text(s.db.getDiagramTitle()).attr("x",it/2).attr("y",n.titleTopMargin).attr("class","titleText");function O(m,x,v){const p=n.barHeight,a=p+n.barGap,d=n.topPadding,f=n.leftPadding,u=ss().domain([0,F.length]).range(["#00B9FA","#F95002"]).interpolate(ds);q(a,d,f,x,v,m,s.db.getExcludes(),s.db.getIncludes()),Y(f,d,x,v),$(m,a,d,f,p,u,x),b(a,d),h(f,d,x,v)}l(O,"makeGantt");function $(m,x,v,p,a,d,f){m.sort((c,S)=>c.vert===S.vert?0:c.vert?1:-1);const u=m.filter(c=>!c.vert),r=[...new Set(u.map(c=>c.order))].map(c=>u.find(S=>S.order===c));z.append("g").selectAll("rect").data(r).enter().append("rect").attr("x",0).attr("y",function(c,S){return S=c.order,S*x+v-2}).attr("width",function(){return f-n.rightPadding/2}).attr("height",x).attr("class",function(c){for(const[S,C]of F.entries())if(c.type===C)return"section section"+S%n.numberSectionStyles;return"section section0"}).enter();const D=z.append("g").selectAll("rect").data(m).enter(),o=s.db.getLinks();if(D.append("rect").attr("id",function(c){return e+"-"+c.id}).attr("rx",3).attr("ry",3).attr("x",function(c){return c.milestone?k(c.startTime)+p+.5*(k(c.endTime)-k(c.startTime))-.5*a:k(c.startTime)+p}).attr("y",function(c,S){return S=c.order,c.vert?n.gridLineStartPadding:S*x+v}).attr("width",function(c){return c.milestone?a:c.vert?.08*a:k(c.renderEndTime||c.endTime)-k(c.startTime)}).attr("height",function(c){return c.vert?u.length*(n.barHeight+n.barGap)+n.barHeight*2:a}).attr("transform-origin",function(c,S){return S=c.order,(k(c.startTime)+p+.5*(k(c.endTime)-k(c.startTime))).toString()+"px "+(S*x+v+.5*a).toString()+"px"}).attr("class",function(c){const S="task";let C="";c.classes.length>0&&(C=c.classes.join(" "));let P=0;for(const[R,W]of F.entries())c.type===W&&(P=R%n.numberSectionStyles);let M="";return c.active?c.crit?M+=" activeCrit":M=" active":c.done?c.crit?M=" doneCrit":M=" done":c.crit&&(M+=" crit"),M.length===0&&(M=" task"),c.milestone&&(M=" milestone "+M),c.vert&&(M=" vert "+M),M+=P,M+=" "+C,S+M}),D.append("text").attr("id",function(c){return e+"-"+c.id+"-text"}).text(function(c){return c.task}).attr("font-size",n.fontSize).attr("x",function(c){let S=k(c.startTime),C=k(c.renderEndTime||c.endTime);if(c.milestone&&(S+=.5*(k(c.endTime)-k(c.startTime))-.5*a,C=S+a),c.vert)return k(c.startTime)+p;const P=this.getBBox().width;return P>C-S?C+P+1.5*n.leftPadding>f?S+p-5:C+p+5:(C-S)/2+S+p}).attr("y",function(c,S){return c.vert?n.gridLineStartPadding+u.length*(n.barHeight+n.barGap)+60:(S=c.order,S*x+n.barHeight/2+(n.fontSize/2-2)+v)}).attr("text-height",a).attr("class",function(c){const S=k(c.startTime);let C=k(c.endTime);c.milestone&&(C=S+a);const P=this.getBBox().width;let M="";c.classes.length>0&&(M=c.classes.join(" "));let R=0;for(const[rt,ot]of F.entries())c.type===ot&&(R=rt%n.numberSectionStyles);let W="";return c.active&&(c.crit?W="activeCritText"+R:W="activeText"+R),c.done?c.crit?W=W+" doneCritText"+R:W=W+" doneText"+R:c.crit&&(W=W+" critText"+R),c.milestone&&(W+=" milestoneText"),c.vert&&(W+=" vertText"),P>C-S?C+P+1.5*n.leftPadding>f?M+" taskTextOutsideLeft taskTextOutside"+R+" "+W:M+" taskTextOutsideRight taskTextOutside"+R+" "+W+" width-"+P:M+" taskText taskText"+R+" "+W+" width-"+P}),ht().securityLevel==="sandbox"){let c;c=bt("#i"+e);const S=c.nodes()[0].contentDocument;D.filter(function(C){return o.has(C.id)}).each(function(C){var P=S.querySelector("#"+CSS.escape(e+"-"+C.id)),M=S.querySelector("#"+CSS.escape(e+"-"+C.id+"-text"));const R=P.parentNode;var W=S.createElement("a");W.setAttribute("xlink:href",o.get(C.id)),W.setAttribute("target","_top"),R.appendChild(W),W.appendChild(P),W.appendChild(M)})}}l($,"drawRects");function q(m,x,v,p,a,d,f,u){if(f.length===0&&u.length===0)return;let _,r;for(const{startTime:C,endTime:P}of d)(_===void 0||C<_)&&(_=C),(r===void 0||P>r)&&(r=P);if(!_||!r)return;if(U(r).diff(U(_),"year")>5){lt.warn("The difference between the min and max time is more than 5 years. This will cause performance issues. Skipping drawing exclude days.");return}const D=s.db.getDateFormat(),o=[];let H=null,c=U(_);for(;c.valueOf()<=r;)s.db.isInvalidDate(c,D,f,u)?H?H.end=c:H={start:c,end:c}:H&&(o.push(H),H=null),c=c.add(1,"d");z.append("g").selectAll("rect").data(o).enter().append("rect").attr("id",C=>e+"-exclude-"+C.start.format("YYYY-MM-DD")).attr("x",C=>k(C.start.startOf("day"))+v).attr("y",n.gridLineStartPadding).attr("width",C=>k(C.end.endOf("day"))-k(C.start.startOf("day"))).attr("height",a-x-n.gridLineStartPadding).attr("transform-origin",function(C,P){return(k(C.start)+v+.5*(k(C.end)-k(C.start))).toString()+"px "+(P*m+.5*a).toString()+"px"}).attr("class","exclude-range")}l(q,"drawExcludeDays");function B(m,x,v,p){if(v<=0||m>x)return 1/0;const a=x-m,d=U.duration({[p??"day"]:v}).asMilliseconds();return d<=0?1/0:Math.ceil(a/d)}l(B,"getEstimatedTickCount");function Y(m,x,v,p){const a=s.db.getDateFormat(),d=s.db.getAxisFormat();let f;d?f=d:a==="D"?f="%d":f=n.axisFormat??"%Y-%m-%d";let u=Xe(k).tickSize(-p+x+n.gridLineStartPadding).tickFormat(Jt(f));const r=/^([1-9]\d*)(millisecond|second|minute|hour|day|week|month)$/.exec(s.db.getTickInterval()||n.tickInterval);if(r!==null){const D=parseInt(r[1],10);if(isNaN(D)||D<=0)lt.warn(`Invalid tick interval value: "${r[1]}". Skipping custom tick interval.`);else{const o=r[2],H=s.db.getWeekday()||n.weekday,c=k.domain(),S=c[0],C=c[1],P=B(S,C,D,o);if(P>Ot)lt.warn(`The tick interval "${D}${o}" would generate ${P} ticks, which exceeds the maximum allowed (${Ot}). This may indicate an invalid date or time range. Skipping custom tick interval.`);else switch(o){case"millisecond":u.ticks(re.every(D));break;case"second":u.ticks(ne.every(D));break;case"minute":u.ticks(ie.every(D));break;case"hour":u.ticks(se.every(D));break;case"day":u.ticks(ee.every(D));break;case"week":u.ticks(ce[H].every(D));break;case"month":u.ticks(te.every(D));break}}}if(z.append("g").attr("class","grid").attr("transform","translate("+m+", "+(p-50)+")").call(u).selectAll("text").style("text-anchor","middle").attr("fill","#000").attr("stroke","none").attr("font-size",10).attr("dy","1em"),s.db.topAxisEnabled()||n.topAxis){let D=es(k).tickSize(-p+x+n.gridLineStartPadding).tickFormat(Jt(f));if(r!==null){const o=parseInt(r[1],10);if(isNaN(o)||o<=0)lt.warn(`Invalid tick interval value: "${r[1]}". Skipping custom tick interval.`);else{const H=r[2],c=s.db.getWeekday()||n.weekday,S=k.domain(),C=S[0],P=S[1];if(B(C,P,o,H)<=Ot)switch(H){case"millisecond":D.ticks(re.every(o));break;case"second":D.ticks(ne.every(o));break;case"minute":D.ticks(ie.every(o));break;case"hour":D.ticks(se.every(o));break;case"day":D.ticks(ee.every(o));break;case"week":D.ticks(ce[c].every(o));break;case"month":D.ticks(te.every(o));break}}}z.append("g").attr("class","grid").attr("transform","translate("+m+", "+x+")").call(D).selectAll("text").style("text-anchor","middle").attr("fill","#000").attr("stroke","none").attr("font-size",10)}}l(Y,"makeGrid");function b(m,x){let v=0;const p=Object.keys(V).map(a=>[a,V[a]]);z.append("g").selectAll("text").data(p).enter().append(function(a){const d=a[0].split(Ve.lineBreakRegex),f=-(d.length-1)/2,u=L.createElementNS("http://www.w3.org/2000/svg","text");u.setAttribute("dy",f+"em");for(const[_,r]of d.entries()){const D=L.createElementNS("http://www.w3.org/2000/svg","tspan");D.setAttribute("alignment-baseline","central"),D.setAttribute("x","10"),_>0&&D.setAttribute("dy","1em"),D.textContent=r,u.appendChild(D)}return u}).attr("x",10).attr("y",function(a,d){if(d>0)for(let f=0;f<d;f++)return v+=p[d-1][1],a[1]*m/2+v*m+x;else return a[1]*m/2+x}).attr("font-size",n.sectionFontSize).attr("class",function(a){for(const[d,f]of F.entries())if(a[0]===f)return"sectionTitle sectionTitle"+d%n.numberSectionStyles;return"sectionTitle"})}l(b,"vertLabels");function h(m,x,v,p){const a=s.db.getTodayMarker();if(a==="off")return;const d=z.append("g").attr("class","today"),f=new Date,u=d.append("line");u.attr("x1",k(f)+m).attr("x2",k(f)+m).attr("y1",n.titleTopMargin).attr("y2",p-n.titleTopMargin).attr("class","today"),a!==""&&u.attr("style",a.replace(/,/g,";"))}l(h,"drawToday");function I(m){const x={},v=[];for(let p=0,a=m.length;p<a;++p)Object.prototype.hasOwnProperty.call(x,m[p])||(x[m[p]]=!0,v.push(m[p]));return v}l(I,"checkUnique")},"draw"),oi={setConf:ni,draw:ai},ci=l(t=>`
  .mermaid-main-font {
        font-family: ${t.fontFamily};
  }

  .exclude-range {
    fill: ${t.excludeBkgColor};
  }

  .section {
    stroke: none;
    opacity: 0.2;
  }

  .section0 {
    fill: ${t.sectionBkgColor};
  }

  .section2 {
    fill: ${t.sectionBkgColor2};
  }

  .section1,
  .section3 {
    fill: ${t.altSectionBkgColor};
    opacity: 0.2;
  }

  .sectionTitle0 {
    fill: ${t.titleColor};
  }

  .sectionTitle1 {
    fill: ${t.titleColor};
  }

  .sectionTitle2 {
    fill: ${t.titleColor};
  }

  .sectionTitle3 {
    fill: ${t.titleColor};
  }

  .sectionTitle {
    text-anchor: start;
    font-family: ${t.fontFamily};
  }


  /* Grid and axis */

  .grid .tick {
    stroke: ${t.gridColor};
    opacity: 0.8;
    shape-rendering: crispEdges;
  }

  .grid .tick text {
    font-family: ${t.fontFamily};
    fill: ${t.textColor};
  }

  .grid path {
    stroke-width: 0;
  }


  /* Today line */

  .today {
    fill: none;
    stroke: ${t.todayLineColor};
    stroke-width: 2px;
  }


  /* Task styling */

  /* Default task */

  .task {
    stroke-width: 2;
  }

  .taskText {
    text-anchor: middle;
    font-family: ${t.fontFamily};
  }

  .taskTextOutsideRight {
    fill: ${t.taskTextDarkColor};
    text-anchor: start;
    font-family: ${t.fontFamily};
  }

  .taskTextOutsideLeft {
    fill: ${t.taskTextDarkColor};
    text-anchor: end;
  }


  /* Special case clickable */

  .task.clickable {
    cursor: pointer;
  }

  .taskText.clickable {
    cursor: pointer;
    fill: ${t.taskTextClickableColor} !important;
    font-weight: bold;
  }

  .taskTextOutsideLeft.clickable {
    cursor: pointer;
    fill: ${t.taskTextClickableColor} !important;
    font-weight: bold;
  }

  .taskTextOutsideRight.clickable {
    cursor: pointer;
    fill: ${t.taskTextClickableColor} !important;
    font-weight: bold;
  }


  /* Specific task settings for the sections*/

  .taskText0,
  .taskText1,
  .taskText2,
  .taskText3 {
    fill: ${t.taskTextColor};
  }

  .task0,
  .task1,
  .task2,
  .task3 {
    fill: ${t.taskBkgColor};
    stroke: ${t.taskBorderColor};
  }

  .taskTextOutside0,
  .taskTextOutside2
  {
    fill: ${t.taskTextOutsideColor};
  }

  .taskTextOutside1,
  .taskTextOutside3 {
    fill: ${t.taskTextOutsideColor};
  }


  /* Active task */

  .active0,
  .active1,
  .active2,
  .active3 {
    fill: ${t.activeTaskBkgColor};
    stroke: ${t.activeTaskBorderColor};
  }

  .activeText0,
  .activeText1,
  .activeText2,
  .activeText3 {
    fill: ${t.taskTextDarkColor} !important;
  }


  /* Completed task */

  .done0,
  .done1,
  .done2,
  .done3 {
    stroke: ${t.doneTaskBorderColor};
    fill: ${t.doneTaskBkgColor};
    stroke-width: 2;
  }

  .doneText0,
  .doneText1,
  .doneText2,
  .doneText3 {
    fill: ${t.taskTextDarkColor} !important;
  }

  /* Done task text displayed outside the bar sits against the diagram background,
     not against the done-task bar, so it must use the outside/contrast color. */
  .doneText0.taskTextOutsideLeft,
  .doneText0.taskTextOutsideRight,
  .doneText1.taskTextOutsideLeft,
  .doneText1.taskTextOutsideRight,
  .doneText2.taskTextOutsideLeft,
  .doneText2.taskTextOutsideRight,
  .doneText3.taskTextOutsideLeft,
  .doneText3.taskTextOutsideRight {
    fill: ${t.taskTextOutsideColor} !important;
  }


  /* Tasks on the critical line */

  .crit0,
  .crit1,
  .crit2,
  .crit3 {
    stroke: ${t.critBorderColor};
    fill: ${t.critBkgColor};
    stroke-width: 2;
  }

  .activeCrit0,
  .activeCrit1,
  .activeCrit2,
  .activeCrit3 {
    stroke: ${t.critBorderColor};
    fill: ${t.activeTaskBkgColor};
    stroke-width: 2;
  }

  .doneCrit0,
  .doneCrit1,
  .doneCrit2,
  .doneCrit3 {
    stroke: ${t.critBorderColor};
    fill: ${t.doneTaskBkgColor};
    stroke-width: 2;
    cursor: pointer;
    shape-rendering: crispEdges;
  }

  .milestone {
    transform: rotate(45deg) scale(0.8,0.8);
  }

  .milestoneText {
    font-style: italic;
  }
  .doneCritText0,
  .doneCritText1,
  .doneCritText2,
  .doneCritText3 {
    fill: ${t.taskTextDarkColor} !important;
  }

  /* Done-crit task text outside the bar — same reasoning as doneText above. */
  .doneCritText0.taskTextOutsideLeft,
  .doneCritText0.taskTextOutsideRight,
  .doneCritText1.taskTextOutsideLeft,
  .doneCritText1.taskTextOutsideRight,
  .doneCritText2.taskTextOutsideLeft,
  .doneCritText2.taskTextOutsideRight,
  .doneCritText3.taskTextOutsideLeft,
  .doneCritText3.taskTextOutsideRight {
    fill: ${t.taskTextOutsideColor} !important;
  }

  .vert {
    stroke: ${t.vertLineColor};
  }

  .vertText {
    font-size: 15px;
    text-anchor: middle;
    fill: ${t.vertLineColor} !important;
  }

  .activeCritText0,
  .activeCritText1,
  .activeCritText2,
  .activeCritText3 {
    fill: ${t.taskTextDarkColor} !important;
  }

  .titleText {
    text-anchor: middle;
    font-size: 18px;
    fill: ${t.titleColor||t.textColor};
    font-family: ${t.fontFamily};
  }
`,"getStyles"),li=ci,vi={parser:xs,db:ii,renderer:oi,styles:li};export{vi as diagram};
