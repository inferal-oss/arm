// Intentional approved substitution of only loadRFB/navigator; native factory/UI/parents unchanged.
import {createScreenViewer as createActual} from '../../internal/server/web/semblance/screen.mjs';
import {loadSyntheticRFB,syntheticNavigator} from './ux-screen-inventory-client.mjs';
export * from '../../internal/server/web/semblance/screen.mjs';
export function createScreenViewer(original){
 const options={...original,loadRFB:loadSyntheticRFB,navigator:syntheticNavigator};
 const referencesExact=['ui','root','token','context','location','window'].every(key=>options[key]===original[key]);
 if(!referencesExact)throw Error('Original screen references changed');
 const actual=createActual(options);
 window.screenInventoryFixture.factories.push({api:actual,bootstrapComplete:original.token===window.__destinationSeed.token,referencesExact});
 return actual;
}
