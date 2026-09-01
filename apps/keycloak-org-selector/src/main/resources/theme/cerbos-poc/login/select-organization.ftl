<#import "template.ftl" as layout>
<@layout.registrationLayout displayMessage=true; section>
    <#if section = "header">
        Select your hospital
    <#elseif section = "form">
        <#-- issue #80: exactly the caller's own memberships, never more,
             never one they do not belong to. The value submitted here is
             re-checked against those same memberships server-side before
             it is ever honoured (OrganizationSelectorAuthenticator#action) -
             this list is a convenience for a legitimate caller, not the
             thing that makes the choice trustworthy. -->
        <form id="kc-select-organization-form" class="${properties.kcFormClass!}" action="${url.loginAction}" method="post">
            <#list organizations as organization>
                <div class="${properties.kcFormGroupClass!}">
                    <label class="${properties.kcLabelClass!}">
                        <input type="radio" name="organization" value="${organization}"<#if organization?index == 0 && !offerTenantWide> checked="checked"</#if>/>
                        ${organization}
                    </label>
                </div>
            </#list>
            <#-- issue #81: an administrator's additional entry, yielding a
                 session with no active hospital at all. Never shown to a
                 user who is not an administrator - action() also re-checks
                 that server-side, since a form field is only ever a
                 convenience, not the thing that makes a choice trustworthy. -->
            <#if offerTenantWide>
                <div class="${properties.kcFormGroupClass!}">
                    <label class="${properties.kcLabelClass!}">
                        <input type="radio" name="organization" value="${tenantWideValue}" checked="checked"/>
                        Tenant-wide (all hospitals)
                    </label>
                </div>
            </#if>
            <div class="${properties.kcFormGroupClass!}">
                <input class="${properties.kcButtonClass!} ${properties.kcButtonPrimaryClass!} ${properties.kcButtonBlockClass!}"
                       type="submit" value="${msg("doSubmit")}"/>
            </div>
        </form>
    </#if>
</@layout.registrationLayout>
