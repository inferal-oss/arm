// Fixture instrumentation of main's one import only; original module/args/API remain intact.
import {createRemoteNodeDashboard as createActual} from '../../internal/server/web/semblance/remote.mjs';
export * from '../../internal/server/web/semblance/remote.mjs';
export function createRemoteNodeDashboard(originalArguments){
 const actual=createActual(originalArguments);
 (window.__destinationDashboards??=[]).push(actual);
 return actual;
}
