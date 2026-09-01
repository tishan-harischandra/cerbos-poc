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
                        <input type="radio" name="organization" value="${organization}"<#if organization?index == 0> checked="checked"</#if>/>
                        ${organization}
                    </label>
                </div>
            </#list>
            <div class="${properties.kcFormGroupClass!}">
                <input class="${properties.kcButtonClass!} ${properties.kcButtonPrimaryClass!} ${properties.kcButtonBlockClass!}"
                       type="submit" value="${msg("doSubmit")}"/>
            </div>
        </form>
    </#if>
</@layout.registrationLayout>
